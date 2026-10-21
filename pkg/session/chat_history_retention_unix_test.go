//go:build linux || darwin

package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// D-X6's separate Linux/macOS unlink control; it is not evidence for native
// Windows. A still-readable open handle must not advertise an already unlinked
// source as retained when the page eventually publishes.
func TestChatHistoryRetention_UnlinkedReadHandleNeedsCoherentReselection(t *testing.T) {
	s := mustStore(t, t.TempDir())
	atDay(s, "2026-01-01")
	_, err := s.Append(ArchiveRecord{TranscriptEntry: TranscriptEntry{ID: "expired", Role: "user", Content: "retention-body", AgentID: "mia", ViewMembership: ViewMembershipChat, Timestamp: s.now()}})
	require.NoError(t, err)
	atDay(s, "2026-10-21")
	_, err = s.Append(ArchiveRecord{TranscriptEntry: TranscriptEntry{ID: "retained", Role: "user", Content: "current", AgentID: "mia", ViewMembership: ViewMembershipChat, Timestamp: s.now()}})
	require.NoError(t, err)
	path := filepath.Join(s.dir(), "2026-01-01.jsonl")
	held, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, held.Close()) }()
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path), "native unlink succeeds with reader handle open")
	got := make([]byte, len(before))
	n, err := held.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, len(before), n)
	require.Equal(t, before, got, "instrument: stale held body remains readable despite actual deletion")
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	t.Fatal("BLOCKED: bounded history read-cut retention-generation/reselection and persisted deleted-catalog seam not implemented — required by D1a/D14/T19/T26/B27/B32/B41/D-X5/X6; after this real unlink and next sweep removed_count0, publish only retained neighbor rows/flags, reconcile once, never leak the held expired body or treat true corruption as expiry")
}
