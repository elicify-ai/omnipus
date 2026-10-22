package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gate0ReleaseRetired(t *testing.T, release any) (error, bool) {
	t.Helper()
	switch fn := release.(type) {
	case func() error:
		return fn(), true
	case func():
		fn() // execute the REAL old callback; absence of its error contract is reported loudly by caller
		return nil, false
	default:
		t.Fatalf("BLOCKED: unknown generation release contract, required by published F4/X")
		return nil, false
	}
}

func gate0RetirementPending(owner *chatHistoryOwner, path string) bool {
	owner.refsMu.Lock()
	defer owner.refsMu.Unlock()
	return owner.retired[path]
}

func gate0PinnedRetiredGeneration(t *testing.T) (*UnifiedStore, string, string, any) {
	t.Helper()
	s, sid, _ := historyRetentionFixture(t, 0)
	require.Len(t, historyRead(t, s, sid, historyOptions(40)).Entries, 40)
	dir := filepath.Join(s.BaseDir(), sid)
	cp, err := historyLoadCheckpoint(context.Background(), dir, sid)
	require.NoError(t, err)
	path := filepath.Join(dir, ChatHistoryIndexDirectory, "generations", cp.File)
	release := any(s.historyOwner(sid).retain(path))
	removed, err := s.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	after, err := historyLoadCheckpoint(context.Background(), dir, sid)
	require.NoError(t, err)
	require.NotEqual(t, cp.File, after.File, "actual retention compaction publishes a different generation")
	require.FileExists(t, path, "actual pin defers retiring the old generation")
	require.True(t, gate0RetirementPending(s.historyOwner(sid), path))
	return s, sid, path, release
}

// F4/X is pinned before assertions: real remove errors return to the caller,
// remain pending, and normal no-new-expiry reads/reconcile retry cleanup.
func TestChatHistoryRetirement_LastReaderErrorsAreRetried(t *testing.T) {
	t.Run("native_remove_error_returns_and_stays_pending", func(t *testing.T) {
		s, sid, path, release := gate0PinnedRetiredGeneration(t)
		clear, nativeError := gate0HoldRetirementRemoveFault(t, path)
		err, reports := gate0ReleaseRetired(t, release)
		t.Run("required_typed_release_error", func(t *testing.T) {
			if !reports {
				t.Fatal("BLOCKED: last-reader release returns void and cannot propagate the actual native remove failure — required by published F4/X")
			}
			require.ErrorIs(t, err, nativeError, "actual OS remove failure must not be swallowed/log-only")
		})
		assert.True(t, gate0RetirementPending(s.historyOwner(sid), path), "failed retirement remains pending")
		require.FileExists(t, path)
		clear()
		require.NoError(t, s.CheckChatHistoryReadiness(context.Background(), sid), "normal zero-mutation reconcile retries cleanup after fault clears")
		assert.NoFileExists(t, path, "X: normal reconcile must retry the deferred failed deletion even with no new expiry")
	})
	t.Run("already_absent_is_success_not_pending_failure", func(t *testing.T) {
		s, sid, path, release := gate0PinnedRetiredGeneration(t)
		require.NoError(t, os.Remove(path))
		err, _ := gate0ReleaseRetired(t, release)
		require.NoError(t, err, "already absent is successful completed cleanup")
		assert.False(t, gate0RetirementPending(s.historyOwner(sid), path), "already absent must not remain a phantom pending failure")
		require.NoError(t, s.CheckChatHistoryReadiness(context.Background(), sid))
	})
	t.Run("actual_page_caller_surfaces_failure_then_normal_read_retries", gate0RetirementReadCaller)
}

type gate0RetirementReadResult struct {
	page ChatHistoryPage
	err  error
}

func gate0RetirementReadCaller(t *testing.T) {
	s, sid, _ := historyRetentionFixture(t, 0)
	dir := filepath.Join(s.BaseDir(), sid)
	cp, err := historyLoadCheckpoint(context.Background(), dir, sid)
	require.NoError(t, err)
	path := filepath.Join(dir, ChatHistoryIndexDirectory, "generations", cp.File)
	captured, release := make(chan struct{}), make(chan struct{})
	var once, free sync.Once
	unblock := func() { free.Do(func() { close(release) }) }
	old := chatHistoryReadHook
	ctx, cancel := context.WithCancel(context.Background())
	chatHistoryReadHook = func(ctx context.Context, event chatHistoryReadEvent) error {
		if event.SessionID == sid && event.Scope == "page" && event.Phase == "cut_captured" {
			once.Do(func() {
				close(captured)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
		return ctx.Err()
	}
	joined := false
	done := make(chan gate0RetirementReadResult, 1)
	go func() {
		p, err := s.ReadChatHistoryPage(ctx, sid, historyOptions(40))
		done <- gate0RetirementReadResult{p, err}
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		if !joined {
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Error("F4 owned actual page did not join after cleanup")
			}
		}
		chatHistoryReadHook = old
	})
	select {
	case <-captured:
	case <-time.After(30 * time.Second):
		t.Fatal("F4 actual reader did not capture/pin its real generation")
	}
	removed, err := s.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	require.FileExists(t, path)
	clear, nativeError := gate0HoldRetirementRemoveFault(t, path)
	unblock()
	var result gate0RetirementReadResult
	select {
	case result = <-done:
		joined = true
	case <-time.After(30 * time.Second):
		t.Fatal("F4 real page did not return after release")
	}
	chatHistoryReadHook = old
	assert.ErrorIs(t, result.err, nativeError, "X: operation triggering release must carry its typed native failure")
	if assert.ErrorIs(t, result.err, ErrChatHistoryUnavailable, "failed cleanup cannot silently return a successful page") {
		var unavailable *ChatHistoryUnavailableError
		require.ErrorAs(t, result.err, &unavailable)
		assert.Equal(t, "storage_error", unavailable.Reason)
	}
	assert.True(t, gate0RetirementPending(s.historyOwner(sid), path))
	clear()
	page := historyRead(t, s, sid, historyOptions(40))
	require.Len(t, page.Entries, 40)
	assert.NoFileExists(t, path, "normal subsequent read retries retirement; no new expiry/publication is required")
}
