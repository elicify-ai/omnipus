package session

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Design §5.8 and §2: a real publication barrier AFTER the builder's source
// validation makes the concurrent write a suffix, not source_changed. A write
// between Prepare's inventories is separately a transient refusal (§1), so it
// would be wrong to expect that different interleaving to heal immediately.
// Watchdog timers below only detect deadlocks; the asserted ordering is actual
// append completion BEFORE the publication barrier is released, not elapsed ms.
func TestChatHistorySelfHeal_AppendDuringRebuildCompletesAndIsIndexed(t *testing.T) {
	f := newSelfHealFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var block, free sync.Once
	unblock := func() { free.Do(func() { close(release) }) }
	old := chatHistoryPublicationHook
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.SessionID == f.sid && e.Point == "before_checkpoint_publish" {
			block.Do(func() { close(entered); <-release })
		}
		if old != nil {
			return old(e)
		}
		return nil
	}
	done := make(chan error, 1)
	joined := false
	go func() { done <- f.store.CheckChatHistoryReadiness(context.Background(), f.sid) }()
	t.Cleanup(func() {
		unblock()
		if !joined {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("healer failed to join after its owned publication barrier was released")
			}
		}
		chatHistoryPublicationHook = old
	})
	select {
	case <-entered:
	case err := <-done:
		joined = true
		t.Fatalf("demand readiness returned before the real rebuild publication barrier: got %v; want a rebuild with a concurrent append", err)
	case <-time.After(10 * time.Second):
		t.Fatal("real rebuild did not reach publication; watchdog is not a latency assertion")
	}
	type appendResult struct {
		addresses []ArchiveAddress
		err       error
	}
	appended := make(chan appendResult, 1)
	go func() {
		var result appendResult
		// Three real accepted facts are below the design's 64-item post-heal
		// suffix bound; the exact count/addresses, not a sample, must survive.
		for i := 0; i < 3; i++ {
			a, err := f.store.AppendTranscriptAddressed(f.sid, historyUserEntry(fmt.Sprintf("during-heal-%d", i), selfHealBody, i))
			if err != nil {
				result.err = err
				break
			}
			result.addresses = append(result.addresses, a)
		}
		appended <- result
	}()
	var result appendResult
	select {
	case result = <-appended:
		require.NoError(t, result.err, "canonical append must finish while rebuild publication remains blocked")
		require.Len(t, result.addresses, 3)
	case <-time.After(10 * time.Second):
		unblock()
		select {
		case <-appended:
		case <-time.After(10 * time.Second):
			t.Error("canonical writer failed to join after releasing rebuild")
		}
		t.Fatal("canonical append queued behind the rebuild; no session shard may be held across rebuild I/O")
	}
	f.addresses = append(f.addresses, result.addresses...)
	unblock()
	select {
	case err := <-done:
		joined = true
		require.NoError(t, err, "post-heal bounded reconcile must include appends after the build's captured end")
	case <-time.After(10 * time.Second):
		t.Fatal("healer did not finish after publication release")
	}
	// Design §2 step 7 / CHECK probe d: inspect the published index BEFORE
	// another demand can repair a skipped proof. All three during-heal rows
	// must already be present when the FIRST heal reports readiness.
	healFixRequireAddresses(t, f.store, f.sid, f.addresses)
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
	selfHealRequireIndexed(t, f)
	selfHealRequirePages(t, f)
}

// Design §5.9 / §3: use an actual page reader at record_opened, with a real
// retained generation and content handle, not merely a manual retain call.
func TestChatHistorySelfHeal_ReaderKeepsOldGenerationUntilContentReadFinishes(t *testing.T) {
	f := selfHealSeed(t)
	// Account for the harmless known-empty catalog fact before pinning the
	// healthy original page. This is not a rebuild or a large-gap demand.
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
	cp := historyCheckpointOf(t, f.store, f.sid)
	oldPath := filepath.Join(historyIndexDir(f.store, f.sid), "generations", cp.File)
	require.FileExists(t, oldPath)
	opened, release := make(chan chatHistoryReadEvent, 1), make(chan struct{})
	var block, free sync.Once
	unblock := func() { free.Do(func() { close(release) }) }
	old := chatHistoryReadHook
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if e.SessionID == f.sid && e.Scope == "page" && e.Stream == "archive" && e.Phase == "record_opened" {
			block.Do(func() {
				opened <- e
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
	type readResult struct {
		page ChatHistoryPage
		err  error
	}
	done := make(chan readResult, 1)
	joined := false
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		page, err := f.store.ReadChatHistoryPage(ctx, f.sid, historyOptions(20))
		done <- readResult{page, err}
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		if !joined {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("pinned reader failed to join after its owned content barrier was released")
			}
		}
		chatHistoryReadHook = old
	})
	var cut chatHistoryReadEvent
	select {
	case cut = <-opened:
		require.Equal(t, cp.Generation, cut.Cut.Generation())
	case result := <-done:
		joined = true
		t.Fatalf("reader returned before opening actual canonical content: %v", result.err)
	case <-time.After(10 * time.Second):
		t.Fatal("real content-open barrier was not reached")
	}
	// Canonical-only appends are the P1 pack's real fsync/crash-residue
	// fixture; they do not publish over the reader's captured root.
	f.addresses = append(f.addresses, historyUnpublishedArchive(t, f.store, f.sid, selfHealBacklog)...)
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "a demand heal must publish while the old reader is still pinned")
	after := historyCheckpointOf(t, f.store, f.sid)
	require.NotEqual(t, cp.File, after.File, "the test must actually replace the generation, not merely leave it unchanged")
	require.FileExists(t, oldPath, "runtime owner refcounts must defer retirement until the real reader closes")
	unblock()
	select {
	case result := <-done:
		joined = true
		require.NoError(t, result.err, "a rebuild must not invalidate a reader halfway through history content I/O")
		require.Equal(t, []string{"selfheal-first"}, historyIDs(result.page), "old cut stays immutable and excludes later appends")
		require.Equal(t, selfHealBody, result.page.Entries[0].Entry.Content)
		require.Equal(t, cut.Cut.Generation(), result.page.Cut.Generation())
	case <-time.After(10 * time.Second):
		t.Fatal("real reader did not finish after content release")
	}
	require.NoFileExists(t, oldPath, "the retired generation is deleted only after the last actual reader releases it")
	selfHealRequireIndexed(t, f)
}
