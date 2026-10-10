package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// U8 r2 F4 (security), re-pinned on the current model store: a chat-only entry
// (view_membership "chat": the connector-reply mirror, the clear marker) never
// enters the model window - not by append, not by being placed as a saved input.
// The model window holds only slots published through the checked model seams;
// ordinary entries given to those seams (the positive control) are present.
func TestChatOnlyEntryNeverEntersTheModelWindow(t *testing.T) {
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	key := meta.ID
	ctx := context.Background()

	_, _, err = store.AppendModelMessage(ctx, key, ModelAppend{
		Message: providers.Message{Role: "user", Content: "ordinary model question"}, ViewMembership: ViewMembershipModel,
		Source: EntrySource{Kind: "user"},
	})
	require.NoError(t, err)

	mirror, err := store.AppendTranscriptAddressed(meta.ID, TranscriptEntry{
		ID: "mirror-1", Role: "assistant", Content: "CHAT-ONLY mirror text", AgentID: "mia",
		Timestamp: time.Now().UTC(), ViewMembership: ViewMembershipChat,
	})
	require.NoError(t, err)

	view, err := store.WindowView(ctx, key)
	require.NoError(t, err)
	var contents []string
	for _, s := range view.Live {
		contents = append(contents, s.Message.Content)
	}
	require.Contains(t, contents, "ordinary model question", "instrument check: the model slot must be in the window")
	require.NotContains(t, contents, "CHAT-ONLY mirror text", "a chat-only entry reached the model window")

	// It cannot be promoted either: placing it as a saved input is refused.
	_, _, err = store.PlaceSavedInput(ctx, key, mirror)
	require.Error(t, err, "a chat-only entry must not be placeable into the model window")
	view, err = store.WindowView(ctx, key)
	require.NoError(t, err)
	require.Len(t, view.Live, 1, "the refused placement must leave the window unchanged")
}
