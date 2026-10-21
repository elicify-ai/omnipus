//go:build windows

package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// T36, D14/D-X6: actual native sharing semantics, not an injected permission
// failure or a Linux cross-build. No native case skips. The held partition is
// first in order so stopping the sweep on it cannot accidentally look green.
func TestChatHistoryRetention_WindowsBusyContinueAndRetry(t *testing.T) {
	s, err := NewArchiveDayStore(t.TempDir(), "windows-history")
	require.NoError(t, err)
	for i, day := range []string{"2026-01-01", "2026-01-02", "2026-01-03", "2026-10-21"} {
		atDay(s, day)
		_, err = s.Append(ArchiveRecord{TranscriptEntry: TranscriptEntry{
			ID: fmt.Sprintf("private-entry-canary-%d", i), Role: "user", Content: "private-body-canary",
			AgentID: "mia", ViewMembership: ViewMembershipChat, Timestamp: s.now(),
		}})
		require.NoError(t, err)
	}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	paths := []string{filepath.Join(s.dir(), "2026-01-01.jsonl"), filepath.Join(s.dir(), "2026-01-02.jsonl"), filepath.Join(s.dir(), "2026-01-03.jsonl")}
	for _, p := range paths {
		require.NoError(t, os.Chtimes(p, old, old))
	}
	held, err := os.Open(paths[0])
	require.NoError(t, err)
	t.Cleanup(func() {
		if held != nil {
			require.NoError(t, held.Close())
		}
	})
	// Instrument positive control: prove this real handle actually prevents
	// unlink on this native runner before interpreting sweep behavior.
	err = os.Remove(paths[0])
	require.True(t, errors.Is(err, windows.ERROR_SHARING_VIOLATION), "native handle must produce sharing-busy, not an unrelated ACL error: %v", err)
	notice, err := s.SweepExpired(90)
	require.NoError(t, err, "D14 sharing-busy skips only this present file and continues")
	require.Equal(t, 2, notice.RemovedPartitions, "later two eligible files must still be processed")
	require.True(t, notice.Expired)
	_, err = os.Stat(paths[0])
	require.NoError(t, err, "busy-present body is not expired")
	for _, p := range paths[1:] {
		_, err = os.Stat(p)
		require.ErrorIs(t, err, os.ErrNotExist, "only actually deleted partitions absent")
	}
	_, err = os.Stat(filepath.Join(s.dir(), archiveCurrentFile))
	require.NoError(t, err, "current append target retained")
	require.NoError(t, held.Close())
	held = nil
	notice, err = s.SweepExpired(90)
	require.NoError(t, err)
	require.Equal(t, 1, notice.RemovedPartitions, "normal next sweep retries once after handle close")
	_, err = os.Stat(paths[0])
	require.ErrorIs(t, err, os.ErrNotExist)
	notice, err = s.SweepExpired(90)
	require.NoError(t, err)
	require.Equal(t, 0, notice.RemovedPartitions, "no fake repeat deletion")
	t.Fatal("BLOCKED: chat retained-set generation and token+rank-only persisted tombstone inspection seam not implemented — required by D2/D14/T36/B50; exact actual removals above must also retire rich lookup/dependency fragments and preserve only token/rank, never entry ID/time/size/body/address")
}

func TestChatHistoryRetention_ActualRetainedContinuation_NativeWindows(t *testing.T) {
	t.Fatal("BLOCKED: native Windows page reader/retained-set generation/tombstone seam not implemented — required by D14/T19/B27/B41/B50; busy-present boundary remains eligible and generation unchanged for its failed removal, later actual deletions advance generation; real permission error remains failure")
}

func TestChatHistoryReadCut_SlowReadDoesNotHoldAppendLocks_NativeWindows(t *testing.T) {
	t.Fatal("BLOCKED: native Windows bounded-record read barrier/handle-close seam not implemented — required by D1/D14/T26/B32/B50; hold a real record read, append before release, prove only its handle defers sweep and the next sweep succeeds after close, never pin all selected partitions for entire page")
}
