//go:build linux || darwin

package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChatHistoryRetention_UnlinkedReadHandleNeedsCoherentReselection(t *testing.T) {
	s, sid := newEffectsSession(t)
	oldTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	a := buildChatHistoryDayFixture(t, s, sid, []chatHistoryFixtureDay{historyDayRows("2026-01-01", "expired", 1, &oldTime), historyDayRows("2026-01-02", "retained", 1, nil)})
	before := historyState(t, s, sid)
	path := filepath.Join(s.BaseDir(), sid, "2026-01-01.jsonl")
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	probe, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, probe.Close()) }()
	opened, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, unblock sync.Once
	var closed atomic.Int64
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
				closed.Add(1)
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
			t.Error("Unix reader did not join")
		}
		chatHistoryReadHook = old
	})
	select {
	case <-opened:
	case <-done:
		require.NoError(t, readErr, "actual content-open barrier")
		t.Fatal("reader never reached actual open file")
	case <-time.After(10 * time.Second):
		t.Fatal("Unix record barrier not reached")
	}
	n, err := s.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 1, n, "actual product sweep unlinks the open rolled file")
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	stillReadable := make([]byte, len(original))
	nRead, err := probe.ReadAt(stillReadable, 0)
	require.NoError(t, err)
	require.Equal(t, len(original), nRead)
	require.Equal(t, original, stillReadable, "instrument: stale held body is readable after native unlink")
	after := historyState(t, s, sid)
	require.NotEqual(t, before.RetentionGeneration, after.RetentionGeneration)
	unblock.Do(func() { close(release) })
	<-done
	require.NoError(t, readErr, "proven expiry reselects rather than permanent storage500")
	require.Empty(t, page.Entries, "held expired bytes must not escape as a successful retained page")
	require.Equal(t, "window_refresh", page.PageInfo.Mode)
	require.False(t, page.HasEarlier)
	require.False(t, page.HasLater)
	require.Equal(t, after.RetentionGeneration, page.PageInfo.RetentionGeneration)
	require.Positive(t, closed.Load())
	_, found, err := s.lookupChatHistoryTombstone(context.Background(), sid, opts.WindowStart)
	require.NoError(t, err)
	require.True(t, found)
	historyAssertNoRichExpiredMetadata(t, s, sid, a[0].EntryID)
	n, err = s.RetentionSweep(90)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, after.RetentionGeneration, historyState(t, s, sid).RetentionGeneration)
}
