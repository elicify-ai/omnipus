package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// These controls deliberately create the possible wrong target first. An
// extractor that guessed a bare/chat ID could otherwise pass against an empty
// store simply because its guessed session did not exist.
//
// The canonical helper is NOT a complete key-grammar validator: its greedy
// :session: cut can select an existing suffix in a chat-scoped or prefixless
// key. Keys are server-generated; no production reachability or exploit has
// been established. That limitation is for Train 3's architect, not an oracle
// certifying the greedy selection here. Unsafe suffix controls below establish
// only non-aliasing to the real candidate, not complete invalid-key refusal.
func TestContinuationTranscript_UnboundKeysDoNotSelectExistingSession(t *testing.T) {
	cfg := newContinueScopeFixConfig(t)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, &scopeFixCountingProvider{})
	t.Cleanup(al.Close)
	store := al.GetSessionStore()
	candidate, err := store.NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	sentinel := session.TranscriptEntry{
		ID: "candidate-sentinel", Role: "user", Content: "This real conversation must remain untouched.",
	}
	require.NoError(t, store.AppendTranscript(candidate.ID, sentinel))
	before, err := store.ReadTranscript(candidate.ID)
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.Equal(t, sentinel.Content, before[0].Content)

	validKey := "agent:" + testDefaultAgentID + ":session:" + candidate.ID
	boundID, boundStore := al.continuationTranscript(validKey)
	require.Equal(t, candidate.ID, boundID, "positive control: the candidate really is resolvable")
	require.Same(t, store, boundStore)

	for _, tc := range []struct {
		name, key string
		unbound   bool
	}{
		{"bare real id", candidate.ID, true},
		{"chat key containing real id", "agent:" + testDefaultAgentID + ":chat:webchat:" + candidate.ID, true},
		{"invalid session separator", "agent:" + testDefaultAgentID + ":session=" + candidate.ID, true},
		{"empty session suffix", "agent:" + testDefaultAgentID + ":session:", true},
		{"missing session suffix", validKey + "-missing", true},
		{"slash in session suffix", validKey + "/child", false},
		{"backslash in session suffix", validKey + "\\child", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, resolved := al.continuationTranscript(tc.key)
			if tc.unbound {
				require.Empty(t, id, "a non-session or missing key must not guess the real candidate")
				require.Nil(t, resolved, "a real candidate must not turn an unbound key into a bound one")
			} else {
				// ResolveSessionStore preserves owning-store errors. Do not call
				// that a clean refusal, or normalize this invalid ID into S.
				require.NotEqual(t, candidate.ID, id, "an invalid suffix must not alias the existing session")
			}
			after, readErr := store.ReadTranscript(candidate.ID)
			require.NoError(t, readErr)
			require.Equal(t, before, after, "identity resolution must not write into the candidate conversation")
		})
	}
}
