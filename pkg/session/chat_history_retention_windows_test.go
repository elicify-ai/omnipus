//go:build windows

package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func historyWindowsHeldRecord(t *testing.T, appendWhileHeld bool) {
	t.Helper()
	s, sid := newEffectsSession(t)
	oldTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	days := []chatHistoryFixtureDay{historyDayRows("2026-01-01", "EXPIRED_WINDOWS_CANARY", 1, &oldTime), historyDayRows("2026-01-02", "second", 1, &oldTime), historyDayRows("2026-01-03", "third", 1, &oldTime), historyDayRows("2026-01-04", "current", 1, nil)}
	a := buildChatHistoryDayFixture(t, s, sid, days)
	before := historyState(t, s, sid)
	opened, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, unblock sync.Once
	var closes atomic.Int64
	old := chatHistoryReadHook
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if e.SessionID == sid && e.Scope == "page" && e.Stream == "archive" && e.Address != nil && e.Address.PartitionKey == "2026-01-01" {
			if e.Phase == "record_opened" {
				once.Do(func() {
					close(opened)
					select {
					case <-release:
					case <-ctx.Done():
					}
				})
			}
			if e.Phase == "record_closed" {
				closes.Add(1)
			}
		}
		return nil
	}
	var page ChatHistoryPage
	var readErr error
	opts := historyOptions(20)
	opts.WindowStart = historyExpectedPosition(sid, a[0])
	opts.WindowEnd = opts.WindowStart
	go func() { defer close(done); page, readErr = s.ReadChatHistoryPage(context.Background(), sid, opts) }()
	t.Cleanup(func() {
		unblock.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Windows record reader did not join")
		}
		chatHistoryReadHook = old
	})
	select {
	case <-opened:
	case <-done:
		require.NoError(t, readErr, "native actual record-open seam")
		t.Fatal("reader returned without real record handle")
	case <-time.After(10 * time.Second):
		t.Fatal("Windows real record-open barrier not reached")
	}
	path := filepath.Join(s.BaseDir(), sid, "2026-01-01.jsonl")
	err := os.Remove(path)
	require.True(t, errors.Is(err, windows.ERROR_SHARING_VIOLATION), "actual production reader handle must be sharing-busy, not a fake hook: %v", err)
	require.True(t, isArchiveSharingBusy(fmt.Errorf("wrapped: %w", err)))
	require.False(t, isArchiveSharingBusy(os.ErrPermission), "genuine permission failure is not expiry/busy")
	n, err := s.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 2, n, "busy first file cannot stop later eligible deletions")
	afterBusy := historyState(t, s, sid)
	require.NotEqual(t, before.RetentionGeneration, afterBusy.RetentionGeneration)
	require.Contains(t, afterBusy.RetainedPartitions, "2026-01-01")
	require.NotContains(t, afterBusy.RetainedPartitions, "2026-01-02")
	require.NotContains(t, afterBusy.RetainedPartitions, "2026-01-03")
	_, found, err := s.lookupChatHistoryTombstone(context.Background(), sid, opts.WindowStart)
	require.NoError(t, err)
	require.False(t, found, "busy-present record is not a tombstone")
	if appendWhileHeld {
		writeDone := make(chan error, 1)
		go func() {
			_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "live-append", Role: "user", Content: "live", AgentID: "mia"})
			writeDone <- err
		}()
		select {
		case err := <-writeDone:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			unblock.Do(func() { close(release) })
			<-writeDone
			t.Fatal("actual history read held append locks")
		}
	}
	unblock.Do(func() { close(release) })
	<-done
	require.NoError(t, readErr)
	require.Equal(t, []string{a[0].EntryID}, historyIDs(page))
	require.Positive(t, closes.Load(), "each actual record handle closes before next ordinary sweep")
	n, err = s.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 1, n, "closed handle permits the next ordinary sweep")
	afterClose := historyState(t, s, sid)
	require.NotEqual(t, afterBusy.RetentionGeneration, afterClose.RetentionGeneration)
	tomb, found, err := s.lookupChatHistoryTombstone(context.Background(), sid, opts.WindowStart)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, opts.WindowStart, tomb.Position)
	historyAssertNoRichExpiredMetadata(t, s, sid, a[0].EntryID)
	n, err = s.RetentionSweep(90)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, afterClose.RetentionGeneration, historyState(t, s, sid).RetentionGeneration)
}

func TestChatHistoryRetention_WindowsBusyContinueAndRetry(t *testing.T) {
	historyWindowsHeldRecord(t, false)
}
func TestChatHistoryRetention_ActualRetainedContinuation_NativeWindows(t *testing.T) {
	historyWindowsHeldRecord(t, false)
}
func TestChatHistoryReadCut_SlowReadDoesNotHoldAppendLocks_NativeWindows(t *testing.T) {
	historyWindowsHeldRecord(t, true)
}
