// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Save-together-or-neither contract for the provenance-paired append, ported to
// the ONE-append design (session-core effects design D6/D7; ARCHITECT-ANSWER-
// U2-EFFECTS.md section 4, "a failed paired append leaves no visible message and
// no Source (D7 truncate)").
//
// Before session-core U2 the message and its provenance were TWO writes: the
// transcript line landed first, then a provenance record, and a failure of the
// second rolled the first back. The source now lives INSIDE the message's own
// line (D6), so there is no second write to fail and no rollback to perform;
// the pair is saved together by construction. What survives — and what this
// test pins — is the OUTCOME the old rollback protected: when the append fails,
// the store surfaces the failure, no half-saved message is visible, and no
// trusted source exists for the failed id.
//
// The failure is real, not a mock: the session's day-mark file
// (transcript.day, the append path's own bookkeeping read) is replaced by a
// DIRECTORY, so the store's attempt to read it fails with a genuine OS error on
// every supported platform and for every user (including root, which permission
// bits would not cover). The real UnifiedStore runs unmodified against real
// files.

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// transcriptBytes reads the session's transcript.jsonl, reporting not-exists
// as nil so both outcomes (file unchanged; file the append created removed
// again) assert uniformly.
func transcriptBytes(t *testing.T, store *UnifiedStore, sessionID string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl"))
	if err != nil {
		require.ErrorIs(t, err, os.ErrNotExist, "unexpected transcript read error")
		return nil
	}
	return raw
}

// sabotageAppend makes the next append to sessionID fail with a real OS error,
// uid-independent: the day-mark path becomes a directory, so the store's own
// read of it fails with EISDIR before a single content byte is written.
func sabotageAppend(t *testing.T, store *UnifiedStore, sessionID string) string {
	t.Helper()
	dayMark := filepath.Join(store.BaseDir(), sessionID, transcriptDayMarkFile)
	// Replace the existing day-mark file (if any) with a directory, so the
	// store's read of it fails with EISDIR rather than "absent".
	if err := os.Remove(dayMark); err != nil {
		require.ErrorIs(t, err, os.ErrNotExist, "instrument: unexpected day-mark remove error")
	}
	require.NoError(t, os.Mkdir(dayMark, 0o700))
	info, err := os.Stat(dayMark)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "instrument control: the append target really is a directory")
	return dayMark
}

// TestAppendTranscriptWithProvenance_FailureLeavesNoVisibleMessage proves the
// one-append pair: a failed paired append leaves the transcript exactly as it
// was, surfaces the error, and persists no trusted source for the failed id.
func TestAppendTranscriptWithProvenance_FailureLeavesNoVisibleMessage(t *testing.T) {
	t.Run("existing transcript is byte-identical after the failure", func(t *testing.T) {
		store := newTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
		require.NoError(t, err)
		sid := meta.ID

		// Prior accepted history on disk — the failed append must not disturb it.
		prior := TranscriptEntry{
			ID: "prior-msg-1", Role: "user", AgentID: "agent-1",
			Content: "accepted earlier", Timestamp: time.Now().UTC(),
		}
		require.NoError(t, store.AppendTranscript(sid, prior), "control: the prior append succeeds")
		before := transcriptBytes(t, store, sid)
		require.NotEmpty(t, before, "control: the prior message is on disk")

		dayMark := sabotageAppend(t, store, sid)

		torn := TranscriptEntry{
			ID: "torn-msg-2", Role: "user", AgentID: "agent-1",
			Content: "must not survive its failed append", Timestamp: time.Now().UTC(),
		}
		err = store.AppendTranscriptWithProvenance(sid, torn, "alice")
		require.Error(t, err, "a failed append must surface the failure, never hide it")
		require.Contains(t, err.Error(), "append transcript with provenance",
			"the error must name the paired-append path that failed")

		require.Equal(t, before, transcriptBytes(t, store, sid),
			"the transcript must be byte-identical — the torn line must not outlive the failed append")

		// Lift the sabotage, then check what the store actually persisted: no
		// source for the failed id, and none of its text on disk.
		require.NoError(t, os.Remove(dayMark))
		_, found, err := store.LookupMessageProvenance(sid, torn.ID)
		require.NoError(t, err)
		require.False(t, found, "no trusted source may exist for the failed message id")
		require.NotContains(t, string(transcriptBytes(t, store, sid)), torn.Content,
			"the failed message's text must not appear anywhere on disk")

		// The store is still usable: the retry lands cleanly as the only
		// new line, with its source.
		require.NoError(t, store.AppendTranscriptWithProvenance(sid, torn, "alice"),
			"the retry after a failed append must succeed")
		got, found, err := store.LookupMessageProvenance(sid, torn.ID)
		require.NoError(t, err)
		require.True(t, found, "the retried message's source must exist")
		require.Equal(t, "alice", got.Principal)
		require.Len(t, linesOf(transcriptBytes(t, store, sid)), 2,
			"exactly the prior message and the retried message — the failure left nothing behind")
	})

	t.Run("first message leaves no transcript line behind", func(t *testing.T) {
		store := newTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
		require.NoError(t, err)
		sid := meta.ID

		// A brand-new UnifiedStore session is born with an EMPTY transcript
		// file (persistNewSessionLocked pre-creates it so readers never
		// error), so the first append's pre-append state is that empty file,
		// not absence — the failure must leave exactly it.
		before := transcriptBytes(t, store, sid)
		require.NotNil(t, before, "control: the session is born with an existing transcript file")
		require.Empty(t, before, "control: the born transcript file is empty")

		dayMark := sabotageAppend(t, store, sid)

		first := TranscriptEntry{
			ID: "torn-first-1", Role: "user", AgentID: "agent-1",
			Content: "first message whose append fails", Timestamp: time.Now().UTC(),
		}
		err = store.AppendTranscriptWithProvenance(sid, first, "bob")
		require.Error(t, err, "a failed append must surface the failure, never hide it")

		require.Equal(t, before, transcriptBytes(t, store, sid),
			"the transcript must be byte-identical to its born-empty state — the torn first line must not survive")

		require.NoError(t, os.Remove(dayMark))
		_, found, err := store.LookupMessageProvenance(sid, first.ID)
		require.NoError(t, err)
		require.False(t, found, "no trusted source may exist for the failed message id")
	})
}

func linesOf(raw []byte) []string {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}
