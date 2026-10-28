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

type healFixPageResult struct {
	page ChatHistoryPage
	err  error
}

// FIX-ROUND-1 row 4 / cr CR-2: physical generation identity must not change
// meaning merely because the store root is spelled through an alias.
func TestChatHistoryHeal_AliasedRootPreservesPinnedReader(t *testing.T) {
	for _, alias := range []string{"symlink", "relative"} {
		t.Run(alias, func(t *testing.T) {
			physical := t.TempDir()
			root := physical
			if alias == "symlink" {
				root = filepath.Join(t.TempDir(), "session-alias")
				if err := os.Symlink(physical, root); err != nil {
					t.Fatalf("BLOCKED: native temporary-root symlink unavailable: %v — required by FIX-ROUND-1 row 4", err)
				}
			} else {
				cwd, err := os.Getwd()
				require.NoError(t, err)
				root, err = filepath.Rel(cwd, physical)
				require.NoError(t, err)
				require.False(t, filepath.IsAbs(root), "the fixture must really pass a relative store root")
			}
			healFixPinnedAliasReader(t, root)
		})
	}
}

func healFixSeedAtRoot(t *testing.T, root string) *selfHealFixture {
	t.Helper()
	s, err := NewUnifiedStore(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	s.stopStatsFlusher()
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	historyStampEmptySessionDay(t, s, meta.ID, historyEarlierDay)
	historyArchiveDay(t, s, meta.ID, historyLaterDay)
	a, err := s.AppendTranscriptAddressed(meta.ID, historyUserEntry("selfheal-first", selfHealBody, 0))
	require.NoError(t, err)
	require.NoError(t, s.CheckChatHistoryReadiness(context.Background(), meta.ID))
	return &selfHealFixture{store: s, sid: meta.ID, addresses: []ArchiveAddress{a}}
}

func healFixPinnedAliasReader(t *testing.T, root string) {
	t.Helper()
	logs := selfHealCaptureLogs(t)
	f := healFixSeedAtRoot(t, root)
	cp := historyCheckpointOf(t, f.store, f.sid)
	oldPath := filepath.Join(historyIndexDir(f.store, f.sid), "generations", cp.File)
	entered, release := make(chan chatHistoryReadEvent, 1), make(chan struct{})
	var block, free sync.Once
	unblock := func() { free.Do(func() { close(release) }) }
	old := chatHistoryReadHook
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if e.SessionID == f.sid && e.Scope == "page" && e.Stream == "archive" && e.Phase == "record_opened" {
			block.Do(func() {
				entered <- e
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
		if old != nil {
			return old(ctx, e)
		}
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan healFixPageResult, 1)
	joined := false
	go func() {
		page, err := f.store.ReadChatHistoryPage(ctx, f.sid, historyOptions(20))
		done <- healFixPageResult{page, err}
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		if !joined {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("aliased-root reader did not join after releasing its content barrier")
			}
		}
		chatHistoryReadHook = old
	})
	var cut chatHistoryReadEvent
	select {
	case cut = <-entered:
		require.Equal(t, cp.Generation, cut.Cut.Generation())
	case result := <-done:
		joined = true
		t.Fatalf("reader returned before actual pinned-content barrier: %v", result.err)
	case <-time.After(10 * time.Second):
		t.Fatal("aliased-root reader never reached its actual content handle")
	}
	f.addresses = append(f.addresses, historyUnpublishedArchive(t, f.store, f.sid, selfHealBacklog)...)
	canonical := partitionBytes(t, f.store, f.sid)
	err := f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
	assert.NoError(t, err, "aliased spelling must not make a pinned-generation heal a hard storage failure")
	assert.FileExists(t, oldPath, "the physical old .chi must remain until the reader releases its real runtime pin")
	unblock()
	select {
	case result := <-done:
		joined = true
		require.NoError(t, result.err)
		require.Equal(t, []string{"selfheal-first"}, historyIDs(result.page))
		require.Equal(t, cut.Cut.Generation(), result.page.Cut.Generation())
	case <-time.After(10 * time.Second):
		t.Fatal("aliased-root reader did not finish after release")
	}
	assert.NoFileExists(t, oldPath, "retirement happens after the real reader's release, in the same path namespace")
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
	healFixRequireAddresses(t, f.store, f.sid, f.addresses)
	require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
}

// FIX-ROUND-1 row 5 / cr CR-3 / pta F1: client lifetime and internal rebuild
// lifetime are separate. A later recoverable gap exposes any hidden hard latch.
func TestChatHistoryHeal_ClientCancellationDoesNotLatchDetachedRebuild(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	canonical := partitionBytes(t, f.store, f.sid)
	base := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	now := base
	f.store.historyHealNow = func() time.Time { return now }
	entered, release := make(chan context.Context, 1), make(chan struct{})
	var block, free sync.Once
	unblock := func() { free.Do(func() { close(release) }) }
	old := chatHistoryReadHook
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if e.SessionID == f.sid && e.Purpose == "suffix" && e.Phase == "record_opened" && e.Address != nil && e.Address.ByteOffset == 0 {
			block.Do(func() {
				entered <- ctx
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
		if old != nil {
			return old(ctx, e)
		}
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan healFixPageResult, 1)
	joined := false
	go func() {
		page, err := f.store.ReadChatHistoryPage(ctx, f.sid, historyOptions(100))
		done <- healFixPageResult{page, err}
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		if !joined {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("cancelled requesting page failed to join after builder release")
			}
		}
		chatHistoryReadHook = old
	})
	var buildCtx context.Context
	select {
	case buildCtx = <-entered:
	case result := <-done:
		joined = true
		t.Fatalf("request returned before the real full-build barrier: %v", result.err)
	case <-time.After(10 * time.Second):
		t.Fatal("real full builder never reached its cancellation barrier")
	}
	cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.NoError(t, buildCtx.Err(), "client cancellation must not cancel the detached internal build")
	unblock()
	select {
	case result := <-done:
		joined = true
		assert.ErrorIs(t, result.err, context.Canceled, "the abandoned page keeps normal request cancellation semantics")
	case <-time.After(10 * time.Second):
		t.Fatal("detached heal/request did not finish after the builder barrier released")
	}
	chatHistoryReadHook = old
	assert.Zero(t, len(chatHistoryHealSlot), "leaving the page must not leak the single heal slot")
	assert.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
	assert.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), "request cancellation is not a permanent readiness failure")
	owner := f.store.historyOwner(f.sid)
	owner.mu.Lock()
	latched := owner.heal.latched
	owner.mu.Unlock()
	assert.False(t, latched, "successful detached recovery must never retain a client-cancellation latch")
	selfHealRequirePages(t, f) // a genuinely fresh open sees every rebuilt row
	f.addresses = append(f.addresses, historyUnpublishedArchive(t, f.store, f.sid, 65)...)
	now = base.Add(60 * time.Second)
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "a later >64 gap must still heal after a healthy reconcile; no hidden permanent latch")
	healFixRequireAddresses(t, f.store, f.sid, f.addresses)
}

// FIX-ROUND-1 row 6 / pta F1: the real 30-second internal deadline must be
// controlled without sleeping or manufacturing a builder result.
func TestChatHistoryHeal_DeadlineIsTransientAndBounded(t *testing.T) {
	// Exact requested seam: UnifiedStore.historyHealWithTimeout
	// func(context.Context, time.Duration) (context.Context, context.CancelFunc),
	// nil => context.WithTimeout. The author will assert requested duration=30s
	// and return a REAL already-expired deadline context, then exercise normal
	// readiness at 0 / 59.999 / 60 / 119.999 / 120 / 180 seconds. Expected failed
	// attempts 1 / 1 / 2 / 2 / 3 / 3; class=deadline, slot released each time,
	// original catchup_limit, retry after_backoff then after_restart on third.
	t.Fatal("BLOCKED: UnifiedStore.historyHealWithTimeout func(context.Context, time.Duration) (context.Context, context.CancelFunc), default context.WithTimeout, not implemented — required by FIX-ROUND-1 row 6 / selfheal-design.md §2 step 4; no real-time 30-second sleep")
}
