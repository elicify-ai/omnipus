package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// SF-11: a corrupt existing index must not hide a held report. Only an absent
// index is the normal empty case; reads must preserve the archived report.
func TestPendingChatInputs_IndexIntegrity(t *testing.T) {
	t.Run("empty_existing_index_errors_and_preserves_held_report", func(t *testing.T) {
		s := newTestStore(t)
		meta, err := s.NewSession(SessionTypeChat, "", "agent")
		require.NoError(t, err)

		const id = "saved-input-sf11-held"
		const body = "helper report held for the parent's next legitimate turn"
		// Saving without placement leaves this report held and chat-only.
		addr, err := s.SaveChatInput(meta.ID, id, "agent", body)
		require.NoError(t, err)
		archive, err := s.archiveStore(meta.ID)
		require.NoError(t, err)
		beforeRaw, before, err := archive.ReadAtRaw(addr)
		require.NoError(t, err)
		require.Equal(t, id, before.ID)
		require.Equal(t, body, before.Content)
		require.Equal(t, ViewMembershipChat, before.ViewMembership)
		require.NotNil(t, before.ModelMessage)
		ordinals, err := archive.OrdinalCount()
		require.NoError(t, err)
		require.Zero(t, ordinals, "the held report has not entered model history")

		pending, err := s.PendingChatInputs(context.Background(), meta.ID)
		require.NoError(t, err)
		require.Equal(t, []TranscriptEntry{before.TranscriptEntry}, pending,
			"SETUP: the healthy index exposes the exact saved report")
		path := s.savedInputIndexPath(meta.ID)
		index, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotEmpty(t, index, "SETUP: an existing populated index is truncated, not an absent file")
		require.NoError(t, os.Truncate(path, 0))

		pending, readErr := s.PendingChatInputs(context.Background(), meta.ID)

		// Check preservation even when the buggy reader returns empty success.
		afterRaw, after, err := archive.ReadAtRaw(addr)
		require.NoError(t, err, "the addressed archive record must remain readable")
		require.Equal(t, beforeRaw, afterRaw, "the saved report bytes must remain unchanged")
		require.Equal(t, before, after, "the complete saved report must remain unchanged")
		index, err = os.ReadFile(path)
		require.NoError(t, err, "a corrupt existing index must not be removed")
		require.Empty(t, index, "the reader must not guess or repair pending state")
		require.Nil(t, pending, "a corrupt index must not supply guessed pending inputs")
		require.EqualError(t, readErr, fmt.Sprintf("saved input index %q: parse: unexpected end of JSON input", meta.ID),
			"an existing zero-byte index is corruption, not a healthy empty queue")
		var syntaxError *json.SyntaxError
		require.ErrorAs(t, readErr, &syntaxError, "the JSON parse error must propagate")
	})

	t.Run("missing_index_returns_empty_without_error", func(t *testing.T) {
		s := newTestStore(t)
		meta, err := s.NewSession(SessionTypeChat, "", "agent")
		require.NoError(t, err)
		path := s.savedInputIndexPath(meta.ID)
		_, err = os.Stat(path)
		require.ErrorIs(t, err, fs.ErrNotExist, "SETUP: this session genuinely has no index")

		pending, err := s.PendingChatInputs(context.Background(), meta.ID)

		require.NoError(t, err, "an absent index is the normal empty case")
		require.Empty(t, pending)
		_, err = os.Stat(path)
		require.ErrorIs(t, err, fs.ErrNotExist, "a read must not invent an empty index file")
	})
}
