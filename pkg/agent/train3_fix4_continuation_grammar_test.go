package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Gate round 4, architect ruling on continuationTranscript: bind a steering
// continuation only for the canonical session-key grammar
// "agent:<agent>:session:<entire nonempty session id>". Any other key kind —
// a chat or peer key that merely CONTAINS ":session:<id>", a prefixless or
// malformed key — stays unbound, even when the trailing id names a real
// session. The real target is created first, so a guessing parser would bind
// it. Cases follow the SinceCursor lane's boundary controls plus the
// architect's examples.
func TestTrain3Fix4_ContinuationTranscriptRequiresCanonicalSessionKey(t *testing.T) {
	cfg := newContinueScopeFixConfig(t)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, &scopeFixCountingProvider{})
	t.Cleanup(al.Close)
	store := al.GetSessionStore()
	target, err := store.NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	before, err := store.ReadTranscript(target.ID)
	require.NoError(t, err)

	canonical := "agent:" + testDefaultAgentID + ":session:" + target.ID
	id, bound := al.continuationTranscript(canonical)
	require.Equal(t, target.ID, id, "positive control: the canonical key binds its session")
	require.Same(t, store, bound)

	for _, tc := range []struct{ name, key string }{
		{"chat peer key containing the marker", "agent:" + testDefaultAgentID + ":chat:telegram:peer:session:" + target.ID},
		{"chat key ending in the real id", "agent:" + testDefaultAgentID + ":chat:webchat:" + target.ID},
		{"prefixless peer key", "peer:session:" + target.ID},
		{"malformed agent prefix", "agnt:" + testDefaultAgentID + ":session:" + target.ID},
		{"empty agent component", "agent::session:" + target.ID},
		{"bare real id", target.ID},
		{"invalid session separator", "agent:" + testDefaultAgentID + ":session=" + target.ID},
		{"empty session id", "agent:" + testDefaultAgentID + ":session:"},
		{"nonexistent session", canonical + "-missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, resolved := al.continuationTranscript(tc.key)
			require.Empty(t, id, "a non-canonical or missing key must stay unbound")
			require.Nil(t, resolved, "no store for an unbound key")
			after, readErr := store.ReadTranscript(target.ID)
			require.NoError(t, readErr)
			require.Equal(t, before, after, "resolution never writes into the real session")
		})
	}
}

// The same grammar governs the two other places that read a session id out of
// a scope key: the turn's attendance classification and the room cost meter.
// A chat/peer or malformed key names no session for either.
func TestTrain3Fix4_AttendanceAndRoomMeterRequireCanonicalSessionKey(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	for _, tc := range []struct{ name, key, want string }{
		{"canonical session key", "agent:mia:session:S1", "S1"},
		{"chat peer key containing the marker", "agent:mia:chat:telegram:peer:session:S1", ""},
		{"prefixless peer key", "peer:session:S1", ""},
		{"malformed agent prefix", "agnt:mia:session:S1", ""},
		{"empty agent component", "agent::session:S1", ""},
		{"empty session id", "agent:mia:session:", ""},
		{"chat key", "agent:mia:chat:webchat:S1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := processOptions{SessionKey: tc.key}
			require.Equal(t, tc.want, attendanceSessionID(&opts), "attendance classifies only a canonical session key")
			m := al.newRoomMeterForOptions(opts)
			require.Equal(t, tc.want, m.sessionID, "the room meter names only a canonical session")
		})
	}
	// A bound transcript session always wins over the key.
	opts := processOptions{SessionKey: "agent:mia:chat:webchat:x", TranscriptSessionID: "T1"}
	require.Equal(t, "T1", attendanceSessionID(&opts))
	require.Equal(t, "T1", al.newRoomMeterForOptions(opts).sessionID)
}
