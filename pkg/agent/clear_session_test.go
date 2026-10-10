package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// FR-030 / BDD-09.2, 09.5: /clear moves the window of a main or extra chat,
// keeps every archive entry, records a marker and starts no session; every
// helper or task session refuses and nothing at all changes.

func clearFixture(t *testing.T, typ session.UnifiedSessionType) (*AgentLoop, *AgentInstance, string) {
	t.Helper()
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	require.NotNil(t, inst.Sessions)
	meta, err := al.GetSessionStore().NewSession(typ, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	inst.Sessions.AddMessage(meta.ID, "user", "first question")
	inst.Sessions.AddMessage(meta.ID, "assistant", "first answer")
	require.NoError(t, al.GetSessionStore().AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID: "seed-1", Role: "user", Content: "first question", AgentID: testDefaultAgentID,
	}))
	require.Len(t, inst.Sessions.GetHistory(meta.ID), 2, "precondition: window holds the conversation")
	return al, inst, meta.ID
}

func TestClearConversation_MainAndExtraChatMoveWindowKeepTranscriptAddMarker(t *testing.T) {
	for _, typ := range []session.UnifiedSessionType{session.SessionTypeChat, session.SessionTypeMain} {
		t.Run(string(typ), func(t *testing.T) {
			al, inst, id := clearFixture(t, typ)
			store := al.GetSessionStore()
			before, err := store.ReadTranscript(id)
			require.NoError(t, err)

			require.NoError(t, al.clearConversation(context.Background(), inst, id, id))

			assert.Empty(t, inst.Sessions.GetHistory(id), "the model window must start empty after /clear")
			after, err := store.ReadTranscript(id)
			require.NoError(t, err)
			require.Len(t, after, len(before)+1, "exactly one marker is appended; nothing is removed")
			for i := range before {
				assert.Equal(t, before[i], after[i], "prior transcript entry %d must be byte-for-byte unchanged", i)
			}
			marker := after[len(after)-1]
			assert.Equal(t, session.EntryTypeSystem, marker.Type)
			assert.Equal(t, clearMarkerText, marker.Content)
			assert.Equal(t, session.ViewMembershipChat, marker.ViewMembership, "the marker is a chat-view row, never model context")
			meta, err := store.GetMeta(id)
			require.NoError(t, err)
			assert.Equal(t, id, meta.ID, "same session id")
		})
	}
}

func TestClearConversation_HelperAndTaskSessionsRefuseWithoutAnyChange(t *testing.T) {
	cases := []struct {
		name  string
		typ   session.UnifiedSessionType
		child bool
	}{
		{"delegate-typed session", session.SessionTypeDelegate, false},
		{"task session", session.SessionTypeTask, false},
		{"chat with a parent session", session.SessionTypeChat, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, inst, id := clearFixture(t, tc.typ)
			store := al.GetSessionStore()
			if tc.child {
				parent, err := store.NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
				require.NoError(t, err)
				require.NoError(t, store.SetMeta(id, session.MetaPatch{ParentSessionID: &parent.ID}))
			}
			before, err := store.ReadTranscript(id)
			require.NoError(t, err)

			err = al.clearConversation(context.Background(), inst, id, id)

			var refused *commands.ClearRefusedError
			require.True(t, errors.As(err, &refused), "want a ClearRefusedError, got %v", err)
			assert.Contains(t, refused.Reason, "main or extra chat")
			assert.Len(t, inst.Sessions.GetHistory(id), 2, "a refused /clear must not move the window")
			after, err := store.ReadTranscript(id)
			require.NoError(t, err)
			assert.Equal(t, before, after, "a refused /clear must not write a marker or touch the transcript")
		})
	}
}

func TestClearConversation_UnreadableSessionIsAVisibleErrorNotEligible(t *testing.T) {
	al, inst, _ := clearFixture(t, session.SessionTypeChat)
	err := al.clearConversation(context.Background(), inst, "nope", "session_does_not_exist")
	require.Error(t, err)
	var refused *commands.ClearRefusedError
	assert.False(t, errors.As(err, &refused), "an unreadable session is an error, not a polite refusal")
}
