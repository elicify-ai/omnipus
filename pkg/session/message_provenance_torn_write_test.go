// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Torn-write contract for the provenance-paired transcript append (founder
// decision 2026-10-04): a web message and its server-side provenance record
// are saved together, or neither is kept. When the provenance record cannot
// be saved after the transcript line landed, AppendTranscriptWithProvenance
// must roll the transcript file back to its exact pre-append state and return
// the error — no half-saved message remains, and the failure is never hidden.
//
// The failure is real, not a mock: the session's provenance record path is
// replaced by a DIRECTORY, so the store's attempt to use its record file
// fails with a genuine OS error on every supported platform and for every
// user (including root, which permission bits would not cover). The real
// UnifiedStore runs unmodified against real files.

package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// transcriptBytes reads the session's transcript.jsonl, reporting not-exists
// as nil so both rollback outcomes (file truncated to prior bytes; file the
// append created removed again) assert uniformly.
func transcriptBytes(t *testing.T, store *UnifiedStore, sessionID string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl"))
	if err != nil {
		require.ErrorIs(t, err, os.ErrNotExist, "unexpected transcript read error")
		return nil
	}
	return raw
}

// TestAppendTranscriptWithProvenance_FailureRollsBackTranscriptLine proves the
// atomic pair: a provenance save failure after the transcript append leaves
// the transcript exactly as it was, surfaces the error, and persists no
// provenance record for the rolled-back message id.
func TestAppendTranscriptWithProvenance_FailureRollsBackTranscriptLine(t *testing.T) {
	t.Run("existing transcript is byte-identical after the failure", func(t *testing.T) {
		store := newTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
		require.NoError(t, err)
		sid := meta.ID

		// Prior accepted history on disk — the rollback must not disturb it.
		prior := TranscriptEntry{
			ID: "prior-msg-1", Role: "user", AgentID: "agent-1",
			Content: "accepted earlier", Timestamp: time.Now().UTC(),
		}
		require.NoError(t, store.AppendTranscript(sid, prior), "control: the prior append succeeds")
		before := transcriptBytes(t, store, sid)
		require.NotEmpty(t, before, "control: the prior message is on disk")

		// Sabotage: the provenance record path becomes a directory, so the
		// record can be neither read nor written — a real OS failure.
		provPath := filepath.Join(store.BaseDir(), sid, provenanceFileName)
		require.NoError(t, os.Mkdir(provPath, 0o700))

		torn := TranscriptEntry{
			ID: "torn-msg-2", Role: "user", AgentID: "agent-1",
			Content: "must not survive its failed provenance record", Timestamp: time.Now().UTC(),
		}
		err = store.AppendTranscriptWithProvenance(sid, torn, "alice")
		require.Error(t, err, "a failed provenance save must fail the append, never hide it")
		require.Contains(t, err.Error(), "record provenance", "the error must name the provenance failure")

		require.Equal(t, before, transcriptBytes(t, store, sid),
			"the transcript must be byte-identical — the torn line must not outlive its failed provenance record")

		// Lift the sabotage, then check what the store actually persisted:
		// no record for the rolled-back id, and an empty provenance history
		// (no record file was ever written).
		require.NoError(t, os.Remove(provPath))
		_, found, err := store.LookupMessageProvenance(sid, torn.ID)
		require.NoError(t, err)
		require.False(t, found, "no provenance record may exist for the rolled-back message id")
	})

	t.Run("first message leaves no transcript line behind", func(t *testing.T) {
		store := newTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
		require.NoError(t, err)
		sid := meta.ID

		// A brand-new UnifiedStore session is born with an EMPTY transcript
		// file (createSessionLocked pre-creates it so readers never error),
		// so the first append's pre-append state is that empty file, not
		// absence — the rollback must restore exactly it.
		before := transcriptBytes(t, store, sid)
		require.NotNil(t, before, "control: the session is born with an existing transcript file")
		require.Empty(t, before, "control: the born transcript file is empty")

		// Sabotage: the provenance record path becomes a directory, so the
		// record can be neither read nor written — a real OS failure.
		provPath := filepath.Join(store.BaseDir(), sid, provenanceFileName)
		require.NoError(t, os.Mkdir(provPath, 0o700))

		first := TranscriptEntry{
			ID: "torn-first-1", Role: "user", AgentID: "agent-1",
			Content: "first message whose provenance save fails", Timestamp: time.Now().UTC(),
		}
		err = store.AppendTranscriptWithProvenance(sid, first, "bob")
		require.Error(t, err, "a failed provenance save must fail the append, never hide it")
		require.Contains(t, err.Error(), "record provenance", "the error must name the provenance failure")

		require.Equal(t, before, transcriptBytes(t, store, sid),
			"the transcript must be byte-identical to its born-empty state — the torn first line must not survive")

		require.NoError(t, os.Remove(provPath))
		_, found, err := store.LookupMessageProvenance(sid, first.ID)
		require.NoError(t, err)
		require.False(t, found, "no provenance record may exist for the rolled-back message id")
	})
}
