package media

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// parkDebounceSave arms store's debounced save with a timer that will not fire
// inside the test, so every ref stored afterwards provably exists only in the
// store's memory for the duration of the test — the exact state a ref is in
// during the saveDebounce window (#1439 Mechanism 2). scheduleSave sees the
// pending timer and does not reschedule; nothing in these tests waits on it.
func parkDebounceSave(s *FileMediaStore) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.saveTimer = time.AfterFunc(time.Hour, s.runScheduledSave)
}

// writePayload creates a small media file inside the isolated media dir and
// returns its path.
func writePayload(t *testing.T, mediaDir, name string) string {
	t.Helper()
	path := filepath.Join(mediaDir, name)
	if err := os.WriteFile(path, []byte("png-bytes "+name), 0o600); err != nil {
		t.Fatalf("write payload %s: %v", name, err)
	}
	return path
}

// TestMediaRefSurvivesReloadSwap reproduces #1439: a ref created on the
// outgoing media store inside the saveDebounce window exists only in that
// store's memory. The gateway's reload-time swap (restartServices.replaceMediaStore)
// installs a fresh store whose LoadRegistry runs BEFORE the old store's final
// Stop flush, so the ref reaches registry.json only after the live store has
// already loaded it — and every consumer that fetches the live pointer per
// request (serveMedia, the upload handlers, the next turn's intake) misses it
// until the next process restart.
//
// The swap below is replaceMediaStore's exact sequence; the first assertion
// resolves through the live store exactly as serveMedia does
// (ResolveWithMetaOpts on the store the live pointer returns). The second
// assertion pins Mechanism 1's other half: the turn engine snapshots the store
// once per turn (turnMediaStore), so the turn in flight across the swap keeps
// resolving through the OLD store — the divergence that made the issue look
// like "the agent's own turn still resolves and uses the same media" while the
// SPA's thumbnail fetch 404s in the same second.
func TestMediaRefSurvivesReloadSwap(t *testing.T) {
	mediaDir := isolatedRegistryHome(t)

	oldStore := NewFileMediaStore()
	parkDebounceSave(oldStore) // ref exists only in old-store memory below

	payload := writePayload(t, mediaDir, "shot.png")
	ref, err := oldStore.Store(payload, MediaMeta{
		Filename: "shot.png",
		Source:   "tool:browser",
	}, SessionInlineScopePrefix+"session-1")
	if err != nil {
		t.Fatalf("old store: %v", err)
	}

	// replaceMediaStore's sequence: fresh store → LoadRegistry → hand the old
	// store's live state over (#1439 fix) → the live pointer swaps to the new
	// store → old store Stop (saves were retired at handover, so this no
	// longer writes registry.json at all).
	liveStore := NewFileMediaStore()
	if err := liveStore.LoadRegistry(); err != nil {
		t.Fatalf("live store LoadRegistry: %v", err)
	}
	oldStore.HandoverTo(liveStore)
	// (production: al.SetMediaStore(liveStore) — every live-pointer consumer
	// from this point on resolves through liveStore.)
	oldStore.Stop()
	// Hygiene rule for every test in this file: no store outlives its test
	// with a save still scheduled — such a timer fires into whichever home
	// is isolated at that moment and races that test's registry expectations.
	t.Cleanup(liveStore.Stop)

	// serveMedia's resolution path, on the live store.
	if _, err := liveStore.Resolve(ref); err != nil {
		t.Fatalf("media ref lost across the reload swap: %v", err)
	}
	// The in-flight turn's snapshot (the old store) must keep resolving too.
	if got, err := oldStore.Resolve(ref); err != nil || got != payload {
		t.Fatalf("in-flight turn lost its ref across the swap: resolve=%q err=%v", got, err)
	}
}

// TestHandoverTo_OldStoreStopNeverOverwritesNewerRegistry pins the second
// half of the #1439 handover contract: after the handover, the outgoing
// store's save machinery is retired, so its Stop() — which the swap calls
// after the live pointer has flipped — can never flush its pre-handover
// snapshot over registry.json once the live store has written a newer one.
// A handover that copied state but left the old store's saves armed would
// clobber refB here (its stale snapshot only knows refA).
func TestHandoverTo_OldStoreStopNeverOverwritesNewerRegistry(t *testing.T) {
	mediaDir := isolatedRegistryHome(t)

	oldStore := NewFileMediaStore()
	parkDebounceSave(oldStore)
	pathA := writePayload(t, mediaDir, "a.png")
	refA, err := oldStore.Store(pathA, MediaMeta{Filename: "a.png", Source: "tool:browser"}, SessionInlineScopePrefix+"session-1")
	if err != nil {
		t.Fatalf("old store: %v", err)
	}

	liveStore := NewFileMediaStore()
	if loadErr := liveStore.LoadRegistry(); loadErr != nil {
		t.Fatalf("live store LoadRegistry: %v", loadErr)
	}
	oldStore.HandoverTo(liveStore)

	// The live store takes a new ref after the swap and persists it with its
	// synchronous final flush.
	pathB := writePayload(t, mediaDir, "b.png")
	refB, err := liveStore.Store(pathB, MediaMeta{Filename: "b.png", Source: "tool:browser"}, SessionInlineScopePrefix+"session-1")
	if err != nil {
		t.Fatalf("live store: %v", err)
	}

	// The swap's tail: old store stops after the pointer already flipped.
	oldStore.Stop()
	liveStore.Stop()
	t.Cleanup(func() {
		oldStore.Stop()
		liveStore.Stop() // idempotent
	})

	refs, err := readPersistedRefs(registryPath())
	if err != nil {
		t.Fatalf("read persisted registry: %v", err)
	}
	if _, ok := refs[refA]; !ok {
		t.Fatalf("refA missing from registry.json after handover")
	}
	if _, ok := refs[refB]; !ok {
		t.Fatalf("refB overwritten by the old store's stale flush after handover")
	}
}

// TestHandoverTo_ReplacesLoadedStaleState pins the wholesale-replace rule: the
// old store's live state is authoritative over what LoadRegistry seeded, so a
// ref the old store no longer holds (released after the file's last snapshot)
// must not resurrect in the new store from the stale file entry.
func TestHandoverTo_ReplacesLoadedStaleState(t *testing.T) {
	mediaDir := isolatedRegistryHome(t)

	// A previous snapshot on disk knows refStale; its file still exists, so
	// LoadRegistry would keep it.
	stalePath := writePayload(t, mediaDir, "stale.png")
	staleSeed := NewFileMediaStore()
	parkDebounceSave(staleSeed)
	refStale, err := staleSeed.Store(stalePath, MediaMeta{Filename: "stale.png", Source: "tool:browser"}, "stale-scope")
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	staleSeed.Stop() // synchronous flush writes refStale to registry.json

	// The outgoing store never held refStale; it holds one live ref.
	oldStore := NewFileMediaStore()
	parkDebounceSave(oldStore)
	pathA := writePayload(t, mediaDir, "live.png")
	refA, err := oldStore.Store(pathA, MediaMeta{Filename: "live.png", Source: "tool:browser"}, SessionInlineScopePrefix+"session-1")
	if err != nil {
		t.Fatalf("old store: %v", err)
	}

	liveStore := NewFileMediaStore()
	if err := liveStore.LoadRegistry(); err != nil {
		t.Fatalf("live store LoadRegistry: %v", err)
	}
	if _, err := liveStore.Resolve(refStale); err != nil {
		t.Fatalf("precondition: loaded registry should resolve refStale: %v", err)
	}

	oldStore.HandoverTo(liveStore)

	if _, err := liveStore.Resolve(refA); err != nil {
		t.Fatalf("handed-over ref not resolvable: %v", err)
	}
	if _, _, err := liveStore.ResolveWithMeta(refStale); err == nil {
		t.Fatalf("released ref resurrected from the stale file entry after handover")
	}
	t.Cleanup(func() {
		liveStore.Stop()
		oldStore.Stop()
	})
}

// TestHandoverTo_NoOpTargets guards the degenerate targets: a nil or self
// destination must be a no-op, leaving the source fully resolvable.
func TestHandoverTo_NoOpTargets(t *testing.T) {
	mediaDir := isolatedRegistryHome(t)
	store := NewFileMediaStore()
	path := writePayload(t, mediaDir, "x.png")
	ref, err := store.Store(path, MediaMeta{Filename: "x.png", Source: "tool:browser"}, "scope")
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	store.HandoverTo(nil) // must not panic
	store.HandoverTo(store)

	if got, err := store.Resolve(ref); err != nil || got != path {
		t.Fatalf("store broken after no-op handover: resolve=%q err=%v", got, err)
	}
	// Store() scheduled a real debounced save above: flush it inside this
	// test's home instead of letting the timer fire into a later test's.
	store.Stop()
}

// TestHandoverTo_RefStoredAfterHandoverResolvesViaLiveStore covers the other
// half of a turn in flight across the reload swap (#1439 follow-up, review
// 6b item 4): the turn does not only RESOLVE refs through its old-store
// snapshot, it also CREATES media through it (browser.screenshot, send_file,
// image generation) after the swap has already flipped the live pointer.
// The ref must land in BOTH stores: the old one keeps the in-flight turn
// working, the live one is what serveMedia and the next turn resolve
// through — and the only store whose save machinery is still armed, so the
// live one must persist it.
func TestHandoverTo_RefStoredAfterHandoverResolvesViaLiveStore(t *testing.T) {
	mediaDir := isolatedRegistryHome(t)

	oldStore := NewFileMediaStore()
	liveStore := NewFileMediaStore()
	oldStore.HandoverTo(liveStore)

	// Media created by the in-flight turn AFTER the swap, through the old
	// store it still holds.
	path := writePayload(t, mediaDir, "late-shot.png")
	ref, err := oldStore.Store(path, MediaMeta{
		Filename: "late-shot.png",
		Source:   "tool:browser",
	}, SessionInlineScopePrefix+"session-2")
	if err != nil {
		t.Fatalf("old store: %v", err)
	}

	// The in-flight turn keeps resolving through its snapshot.
	if got, resolveErr := oldStore.Resolve(ref); resolveErr != nil || got != path {
		t.Fatalf("in-flight turn lost its own new ref: resolve=%q err=%v", got, resolveErr)
	}
	// serveMedia / next-turn path: the live store must know the same ref.
	if got, resolveErr := liveStore.Resolve(ref); resolveErr != nil {
		t.Fatalf("ref created after the handover never reached the live store: %v", resolveErr)
	} else if got != path {
		t.Fatalf("live store resolves the forwarded ref to the wrong path: %q", got)
	}

	// Persistence: the live store is the only store with saves armed, so its
	// final flush is what must carry the ref across a restart.
	liveStore.Stop()
	oldStore.Stop()
	refs, err := readPersistedRefs(registryPath())
	if err != nil {
		t.Fatalf("read persisted registry: %v", err)
	}
	if rec, ok := refs[ref]; !ok {
		t.Fatalf("ref created after the handover is not persisted by the live store")
	} else if rec.Path != path {
		t.Fatalf("persisted ref points at %q, want %q", rec.Path, path)
	}
}

// TestHandoverTo_HandedOverRefsArePersisted pins the persistence half of the
// handover itself: the outgoing store's save machinery is retired at
// handover, so unless the handover arms a save on the live store, the refs
// it just moved exist only in memory — registry.json still holds the
// pre-handover snapshot (missing exactly the last saveDebounce window the
// handover exists to rescue), and a restart before the live store's next
// mutation loses them.
func TestHandoverTo_HandedOverRefsArePersisted(t *testing.T) {
	mediaDir := isolatedRegistryHome(t)

	oldStore := NewFileMediaStore()
	parkDebounceSave(oldStore)
	path := writePayload(t, mediaDir, "handed-over.png")
	ref, err := oldStore.Store(path, MediaMeta{
		Filename: "handed-over.png",
		Source:   "tool:browser",
	}, SessionInlineScopePrefix+"session-3")
	if err != nil {
		t.Fatalf("old store: %v", err)
	}

	liveStore := NewFileMediaStore()
	oldStore.HandoverTo(liveStore)
	liveStore.Stop() // synchronous final flush of the live store

	oldStore.Stop()
	refs, err := readPersistedRefs(registryPath())
	if err != nil {
		t.Fatalf("read persisted registry: %v", err)
	}
	if rec, ok := refs[ref]; !ok {
		t.Fatalf("handed-over ref lost on restart: live store never persisted it")
	} else if rec.Path != path {
		t.Fatalf("persisted ref points at %q, want %q", rec.Path, path)
	}
}
