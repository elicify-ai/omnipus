package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// T62/B73 R2-I1: exercise the real assistant writer, which intentionally omits
// entry.Type, rather than only hand-authoring an empty type in a session test.
func TestChatHistoryDisplay_RealIntermediateAssistantWriter(t *testing.T) {
	store := u3NewRealStore(t)
	meta, err := store.NewSession(session.SessionTypeChat, "", "mia")
	require.NoError(t, err)
	require.NoError(t, store.AddMessage(meta.ID, "user", "MODEL_CONTEXT_CANARY"))
	modelBefore := store.GetHistory(meta.ID)
	require.Len(t, modelBefore, 1, "positive control: the model window is not empty")
	require.Equal(t, "MODEL_CONTEXT_CANARY", modelBefore[0].Content)
	modelSnapshot, err := json.Marshal(modelBefore)
	require.NoError(t, err)
	ts := &turnState{sessionKey: "d2-real-writer", turnID: "d2-writer-turn", transcriptStore: store, transcriptSessionID: meta.ID}
	id := ts.nextRoundMessageID()
	full := strings.Repeat("x", 65534) + "€" //65537 bytes, character straddles65536.
	ts.appendIntermediateAssistantTranscriptImpl(full, "")
	canonical, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, canonical, 1)
	require.Equal(t, id, canonical[0].ID)
	require.Equal(t, session.EntryType(""), canonical[0].Type, "positive control: actual ordinary assistant producer omits type")
	require.Equal(t, full, canonical[0].Content, "canonical/model text remains full")
	p, err := store.ReadChatHistoryPage(context.Background(), meta.ID, session.ChatHistoryPageOptions{Limit: 20, Weigh: func(_ context.Context, p session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
		n := int64(256 + len(p.Entries)*128)
		return session.ChatHistoryWeight{RESTBytes: n, WSBytes: n, ProjectedBytes: n}, nil
	}})
	require.NoError(t, err)
	require.Len(t, p.Entries, 1)
	require.Equal(t, strings.Repeat("x", 65534), p.Entries[0].Entry.Content, "A2.1 empty-type ordinary assistant is shortened on the real display read")
	after, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Equal(t, canonical, after, "display read cannot change the canonical transcript")
	modelAfter, err := json.Marshal(store.GetHistory(meta.ID))
	require.NoError(t, err)
	require.Equal(t, modelSnapshot, modelAfter, "the model's actual live context is unchanged, including in-place mutations")
}
