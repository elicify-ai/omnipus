package media

import (
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestReview6b_HandoverStoreDoesNotDoubleCount pins 6b review finding L1.
// Store registers a ref on the old store (storeRef) and only then forwards it
// to successors (forwardToSuccessors); no common lock spans the two steps. A
// HandoverTo landing between them copies the ref and its path count into the
// live store, and the forward then registers the same ref there again. A
// unique ref must count exactly once, so releasing its only scope deletes the
// store-managed file.
func TestReview6b_HandoverStoreDoesNotDoubleCount(t *testing.T) {
	dir := isolatedRegistryHome(t)
	old, live := NewFileMediaStore(), NewFileMediaStore()
	parkDebounceSave(old)
	parkDebounceSave(live)
	defer old.Stop()
	defer live.Stop()
	path := writePayload(t, dir, "overlap.png")
	const ref = "media://review-overlap"
	const scope = "review-scope"
	meta := MediaMeta{Filename: "overlap.png", CleanupPolicy: CleanupPolicyDeleteOnCleanup}

	// Store step 1, then HandoverTo, then Store step 2.
	old.storeRef(ref, path, meta, scope)
	old.HandoverTo(live)
	old.forwardToSuccessors(ref, path, meta, scope)

	live.mu.RLock()
	count := live.pathStates[path].refCount
	refs := len(live.refs)
	live.mu.RUnlock()
	if refs != 1 || count != 1 {
		t.Errorf("one unique ref has refs=%d refCount=%d, want 1 each", refs, count)
	}
	if err := live.ReleaseAll(scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("last ref released but managed file was retained: stat error=%v", err)
	}
}

// TestReview6b_QueuedOldSaveCannotOverwriteLiveRegistry pins 6b review finding
// I1. A debounce callback on the old store that passed its saveStopped check
// BEFORE HandoverTo retired the store, and is queued on registryMu while the
// handover holds it, must not write the retired store's stale snapshot over a
// newer one the live store has persisted after the handover.
func TestReview6b_QueuedOldSaveCannotOverwriteLiveRegistry(t *testing.T) {
	dir := isolatedRegistryHome(t)
	old, live := NewFileMediaStore(), NewFileMediaStore()
	parkDebounceSave(old)
	parkDebounceSave(live)
	pathA := writePayload(t, dir, "old.png")
	refA, err := old.Store(pathA, MediaMeta{Filename: "old.png"}, "scope-a")
	if err != nil {
		t.Fatal(err)
	}

	// Park the FIRST registry write (if any) until the test releases it.
	originalWrite := writeRegistryFile
	blocked, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	writeRegistryFile = func(path string, data []byte, mode os.FileMode) error {
		first := false
		once.Do(func() { first = true })
		if first {
			close(blocked)
			<-resume
		}
		return originalWrite(path, data, mode)
	}
	var resumeOnce sync.Once
	unblock := func() { resumeOnce.Do(func() { close(resume) }) }
	t.Cleanup(func() {
		unblock()
		old.Stop()
		live.Stop()
		writeRegistryFile = originalWrite
	})

	// Make HandoverTo take old.registryMu and park on live.registryMu.
	live.registryMu.Lock()
	handed := make(chan struct{})
	go func() { old.HandoverTo(live); close(handed) }()
	deadline := time.Now().Add(5 * time.Second)
	for old.registryMu.TryLock() {
		old.registryMu.Unlock()
		if time.Now().After(deadline) {
			live.registryMu.Unlock()
			t.Fatal("handover never acquired old registry lock")
		}
		runtime.Gosched()
	}

	// The old store's debounce callback fires now: it passes saveStopped
	// (not yet set) and queues on old.registryMu behind the handover.
	old.saveMu.Lock()
	old.saveTimer.Stop()
	old.saveMu.Unlock()
	saved := make(chan struct{})
	go func() { old.runScheduledSave(); close(saved) }()
	for {
		old.saveMu.Lock()
		started := old.saveTimer == nil && !old.saveStopped
		old.saveMu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			live.registryMu.Unlock()
			t.Fatal("old callback never passed saveStopped")
		}
		runtime.Gosched()
	}
	live.registryMu.Unlock()
	select {
	case <-handed:
	case <-time.After(5 * time.Second):
		t.Fatal("handover never completed")
	}

	// Either the retired store's queued save reaches the write (and parks in
	// the hook), or it refuses to write and returns. Both are observed below.
	oldWriteParked := false
	select {
	case <-blocked:
		oldWriteParked = true
	case <-saved:
	case <-time.After(5 * time.Second):
		t.Fatal("queued old save neither wrote nor returned")
	}

	pathB := writePayload(t, dir, "new.png")
	refB, err := live.Store(pathB, MediaMeta{Filename: "new.png"}, "scope-b")
	if err != nil {
		t.Fatal(err)
	}
	if !oldWriteParked {
		// The parking hook is armed for the first write; let the live save
		// through instead of parking it.
		unblock()
	}
	live.SaveRegistry()
	before, err := readPersistedRefs(registryPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := before[refB]; !ok {
		t.Fatal("precondition: live ref was not persisted")
	}
	unblock()
	select {
	case <-saved:
	case <-time.After(5 * time.Second):
		t.Fatal("old callback never returned")
	}
	after, err := readPersistedRefs(registryPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after[refA]; !ok {
		t.Error("old ref missing from the persisted registry")
	}
	if _, ok := after[refB]; !ok {
		t.Fatal("retired queued save overwrote the live registry; newest ref lost on restart")
	}
}
