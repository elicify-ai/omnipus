package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Oracle: FR-024 - Stop labels a discarded input; the archived bytes are
// untouched; only the labelled entry carries input_disposition; recording twice
// is idempotent; the entry's own client_message_id fills the projection.
func TestInputDisposition_LabelsOnlyTheDiscardedEntryAndLeavesArchiveBytes(t *testing.T) {
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	for _, e := range []TranscriptEntry{
		{ID: "m1", Role: "user", Content: "delivered", AgentID: "mia", Timestamp: time.Now().UTC(), ClientMessageID: "c1"},
		{ID: "m2", Role: "user", Content: "discarded", AgentID: "mia", Timestamp: time.Now().UTC(), ClientMessageID: "c2"},
	} {
		require.NoError(t, store.AppendTranscript(meta.ID, e))
	}
	archive := filepath.Join(store.BaseDir(), meta.ID, "transcript.jsonl")
	before, err := os.ReadFile(archive)
	require.NoError(t, err)

	require.NoError(t, store.RecordInputDiscarded(meta.ID, "m2", ""))
	require.NoError(t, store.RecordInputDiscarded(meta.ID, "m2", ""), "idempotent")

	after, err := os.ReadFile(archive)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "the archived bytes are preserved")

	entries, err := store.ReadTranscriptWithDispositions(meta.ID)
	require.NoError(t, err)
	byID := map[string]TranscriptEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	require.Nil(t, byID["m1"].InputDisposition, "a delivered message has no disposition")
	d := byID["m2"].InputDisposition
	require.NotNil(t, d)
	require.Equal(t, InputDisposition{MessageID: "m2", ClientMessageID: "c2", State: "discarded", Reason: "stopped_before_delivery"}, *d)

	plain, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	for _, e := range plain {
		require.Nil(t, e.InputDisposition, "the plain archive read never carries the projection")
	}
	lines, _ := os.ReadFile(filepath.Join(store.BaseDir(), meta.ID, inputDispositionFileName))
	require.Equal(t, 1, countDispositionLines(lines), "recording twice wrote one record")
}

func countDispositionLines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
