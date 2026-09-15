package media

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Timing margins for the debounced-save tests below. They only bound how long a
// test waits for the saveDebounce timer to fire or for a goroutine to queue on a
// lock. A stalled machine can make a test MISS a regression (the ordering it
// forces did not happen in time); it can never make a correct implementation fail.
const (
	debounceFireMargin = 300 * time.Millisecond
	lockQueueMargin    = 50 * time.Millisecond
	stopBlockWindow    = 150 * time.Millisecond
	orderingWindow     = 750 * time.Millisecond
	persistDeadline    = 5 * time.Second
	persistPollEvery   = 20 * time.Millisecond
)

// captureRegistryFailures routes pkg/logger to a per-test file and returns a
// function listing every "registry: ... failed" line logged so far. SaveRegistry
// is best-effort and reports failures only through the log, so this is how a test
// proves that no write or rename failed.
func captureRegistryFailures(t *testing.T) func() []string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "media-test.log")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)
	return func() []string {
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read captured log: %v", err)
		}
		var failures []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, `"message":"registry: `) && strings.Contains(line, ` failed"`) {
				failures = append(failures, line)
			}
		}
		return failures
	}
}

// isolatedRegistryHome pins OMNIPUS_HOME to a fresh per-test directory and
// returns the media directory registryPath() now resolves into.
func isolatedRegistryHome(t *testing.T) string {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	dir := TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir media dir: %v", err)
	}
	return dir
}

// readPersistedRefs decodes registry.json straight from disk. A registry that
// does not exist yet reads as empty.
func readPersistedRefs(path string) (map[string]registryRecord, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]registryRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var rf registryFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return rf.Entries, nil
}

// TestSaveRegistry_ConcurrentSavesNeverCorruptOrRegress is the regression test
// for the macOS CI failure "LoadRegistry: media registry: parse .../registry.json:
// invalid character 'e' after top-level value" (release/v0.1.1, Cross-Platform
// run 34940774580). SaveRegistry wrote every snapshot through ONE fixed temp path
// (registry.json.tmp) with nothing ordering its callers. Concurrent saves
// truncated and overwrote each other's temp file, so a short snapshot followed by
// the tail of a longer one was renamed into place, the losing rename failed with
// ENOENT, and an older snapshot could land after a newer one.
//
// Here concurrent SaveRegistry calls race a mutator that grows AND shrinks the
// store (so snapshots differ in length) and a reader that reloads the registry
// throughout. Required: every reload parses, no save logs a failure, no temp file
// is left behind, and once everything has finished the file on disk holds exactly
// the refs the mutation script left live. The mutator's own last save starts
// after its last mutation, so any other final content means an older snapshot
// overwrote a newer one.
func TestSaveRegistry_ConcurrentSavesNeverCorruptOrRegress(t *testing.T) {
	registryFailures := captureRegistryFailures(t)
	dir := isolatedRegistryHome(t)
	store := NewFileMediaStore()
	t.Cleanup(store.Stop)

	const (
		savers        = 4
		savesPerSaver = 4
		mutations     = 9
		reloads       = 10
		// Steps i = 2, 5, 8 each release the previous step's scope:
		// 9 stored - 3 released.
		wantLive = 6
	)
	paths := make([]string, mutations)
	for i := range paths {
		paths[i] = createTempFile(t, dir, fmt.Sprintf("file-%02d.bin", i))
	}

	type liveRef struct{ ref, path string }
	// live (scope -> ref) is written only by the mutator goroutine and read only
	// after wg.Wait returns.
	live := make(map[string]liveRef)

	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i, p := range paths {
			scope := fmt.Sprintf("scope-%02d", i)
			ref, err := store.Store(p, MediaMeta{
				Filename:      filepath.Base(p),
				CleanupPolicy: CleanupPolicyForgetOnly, // ReleaseAll must not delete the backing file
			}, scope)
			if err != nil {
				t.Errorf("Store(%s): %v", p, err)
				return
			}
			live[scope] = liveRef{ref: ref, path: p}
			if i%3 == 2 {
				prev := fmt.Sprintf("scope-%02d", i-1)
				if err := store.ReleaseAll(prev); err != nil {
					t.Errorf("ReleaseAll(%s): %v", prev, err)
					return
				}
				delete(live, prev)
			}
			store.SaveRegistry()
		}
	}()

	for range savers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range savesPerSaver {
				store.SaveRegistry()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for range reloads {
			if err := NewFileMediaStore().LoadRegistry(); err != nil {
				t.Errorf("LoadRegistry while saves are running: %v", err)
				return
			}
		}
	}()

	close(start)
	wg.Wait()
	// Settle before inspecting the disk: flush the debounced save the mutations
	// scheduled and wait for any save still in flight.
	store.Stop()

	if failures := registryFailures(); len(failures) > 0 {
		t.Errorf("%d registry save failure(s) logged; first: %s", len(failures), failures[0])
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read media dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind in media dir: %s", e.Name())
		}
	}

	persisted, err := readPersistedRefs(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatalf("registry on disk after concurrent saves: %v", err)
	}
	if len(live) != wantLive {
		t.Fatalf("mutation script left %d live refs, want %d (script bug)", len(live), wantLive)
	}
	if len(persisted) != wantLive {
		t.Errorf("registry on disk holds %d refs, want exactly %d", len(persisted), wantLive)
	}

	reloaded := NewFileMediaStore()
	if err := reloaded.LoadRegistry(); err != nil {
		t.Fatalf("LoadRegistry after concurrent saves: %v", err)
	}
	for scope, want := range live {
		got, err := reloaded.Resolve(want.ref)
		if err != nil {
			t.Errorf("%s: ref %s missing after reload (an older snapshot overwrote a newer one): %v",
				scope, want.ref, err)
			continue
		}
		if got != want.path {
			t.Errorf("%s: Resolve(%s) = %q, want %q", scope, want.ref, got, want.path)
		}
	}
}

// TestSaveRegistry_StoresSharingOneHomeNeverInterleave reproduces the exact
// shape of the CI failure: a leftover store with an EMPTY registry (an earlier
// test's store whose debounced save fired late) saving into the same
// OMNIPUS_HOME as a store that holds refs. registryMu is per store and cannot
// order these two writers; only replacing the file through a uniquely named temp
// file keeps each write whole. With the old fixed registry.json.tmp, the 26-byte
// empty snapshot {"version":1,"entries":{}} written over a longer one left byte
// 26 of the longer one (the "e" of "media://) straight after a complete JSON
// value: "invalid character 'e' after top-level value".
func TestSaveRegistry_StoresSharingOneHomeNeverInterleave(t *testing.T) {
	registryFailures := captureRegistryFailures(t)
	dir := isolatedRegistryHome(t)

	withRefs := NewFileMediaStore()
	t.Cleanup(withRefs.Stop)
	wantRefs := make([]string, 0, 2)
	for i := range 2 {
		p := createTempFile(t, dir, fmt.Sprintf("shared-%d.jpg", i))
		ref, err := withRefs.Store(p, MediaMeta{
			Filename:      filepath.Base(p),
			CleanupPolicy: CleanupPolicyForgetOnly,
		}, "scope-shared")
		if err != nil {
			t.Fatalf("Store(%s): %v", p, err)
		}
		wantRefs = append(wantRefs, ref)
	}
	empty := NewFileMediaStore()
	t.Cleanup(empty.Stop)

	const (
		savesPerStore = 10
		reloads       = 10
	)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, s := range []*FileMediaStore{withRefs, empty} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range savesPerStore {
				s.SaveRegistry()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for range reloads {
			if err := NewFileMediaStore().LoadRegistry(); err != nil {
				t.Errorf("LoadRegistry while two stores are saving: %v", err)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	withRefs.Stop() // settle the debounced save scheduled by the Store calls

	if failures := registryFailures(); len(failures) > 0 {
		t.Errorf("%d registry save failure(s) logged; first: %s", len(failures), failures[0])
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read media dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind in media dir: %s", e.Name())
		}
	}

	persisted, err := readPersistedRefs(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatalf("registry on disk after two stores saved into one home: %v", err)
	}
	// The last complete snapshot renamed into place wins, so the file is exactly
	// one store's snapshot: the empty one, or both refs of the other.
	switch len(persisted) {
	case 0:
	case len(wantRefs):
		for _, ref := range wantRefs {
			if _, ok := persisted[ref]; !ok {
				t.Errorf("registry holds %d refs but not %s: a mix of two snapshots", len(persisted), ref)
			}
		}
	default:
		t.Errorf("registry holds %d refs, want 0 or %d (exactly one store's snapshot)", len(persisted), len(wantRefs))
	}
}

// TestSaveRegistry_LaterSaveNeverOverwrittenByEarlierSnapshot pins the ordering
// half of the fix: a save whose snapshot was taken earlier must never reach disk
// after a save that started later. The first save is parked after it has taken
// and marshalled its snapshot; a ref is then stored and a second save starts.
// When saves on one store are serialized (snapshot and write both under
// registryMu), the second save cannot start until the first has written, so the
// newer snapshot is written last and stays on disk. When they are not, the
// second save writes first and the parked, older snapshot then overwrites it,
// silently dropping the new ref from the registry: gone after the next restart.
func TestSaveRegistry_LaterSaveNeverOverwrittenByEarlierSnapshot(t *testing.T) {
	dir := isolatedRegistryHome(t)
	store := NewFileMediaStore()
	t.Cleanup(store.Stop)
	forgetOnly := MediaMeta{CleanupPolicy: CleanupPolicyForgetOnly}

	older := createTempFile(t, dir, "older.jpg")
	olderRef, err := store.Store(older, forgetOnly, "scope-older")
	if err != nil {
		t.Fatalf("Store(older): %v", err)
	}
	// Stop now: it persists olderRef and turns off debounced saving, so no timer
	// can add a third, unscheduled save to the sequence below. SaveRegistry
	// itself is not gated by Stop.
	store.Stop()

	firstParked := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondWritten := make(chan struct{})
	var writes atomic.Int32
	realWrite := writeRegistryFile
	writeRegistryFile = func(path string, data []byte, perm os.FileMode) error {
		switch writes.Add(1) {
		case 1:
			close(firstParked)
			<-releaseFirst
		case 2:
			defer close(secondWritten)
		}
		return realWrite(path, data, perm)
	}
	t.Cleanup(func() { writeRegistryFile = realWrite })

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		store.SaveRegistry() // snapshot: olderRef only
	}()
	select {
	case <-firstParked:
	case <-time.After(persistDeadline):
		close(releaseFirst)
		t.Fatalf("first save never reached its write within %s", persistDeadline)
	}

	newer := createTempFile(t, dir, "newer.jpg")
	newerRef, err := store.Store(newer, forgetOnly, "scope-newer")
	if err != nil {
		close(releaseFirst)
		t.Fatalf("Store(newer): %v", err)
	}
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		store.SaveRegistry() // snapshot must include newerRef
	}()

	select {
	case <-secondWritten:
		t.Errorf("a later save wrote while an earlier save was parked between its snapshot and its write: " +
			"saves on one store are not serialized")
	case <-time.After(orderingWindow):
	}
	close(releaseFirst)
	for name, done := range map[string]chan struct{}{"first": firstDone, "second": secondDone} {
		select {
		case <-done:
		case <-time.After(persistDeadline):
			t.Fatalf("%s save did not finish within %s of the first save being released", name, persistDeadline)
		}
	}

	persisted, err := readPersistedRefs(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatalf("registry after both saves: %v", err)
	}
	if rec, ok := persisted[olderRef]; !ok || rec.Path != older {
		t.Errorf("registry entry for %s = %+v (present=%v), want path %q", olderRef, rec, ok, older)
	}
	if rec, ok := persisted[newerRef]; !ok || rec.Path != newer {
		t.Errorf("registry entry for %s = %+v (present=%v), want path %q: "+
			"the earlier snapshot overwrote the later one", newerRef, rec, ok, newer)
	}
}

// TestStop_WaitsForInFlightScheduledSave pins Stop as the point after which a
// store never writes again. A debounced save that has already fired must finish
// before Stop returns. Otherwise its write can land after Stop: in tests, inside
// a directory a later test now owns (the cross-test leak behind the same CI
// failure); in the gateway, after shutdown or a reload has moved on.
func TestStop_WaitsForInFlightScheduledSave(t *testing.T) {
	registryFailures := captureRegistryFailures(t)
	dir := isolatedRegistryHome(t)
	store := NewFileMediaStore()
	t.Cleanup(store.Stop)

	p := createTempFile(t, dir, "in-flight.jpg")
	ref, err := store.Store(p, MediaMeta{
		Filename:      "in-flight.jpg",
		CleanupPolicy: CleanupPolicyForgetOnly,
	}, "scope-in-flight")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Hold the write lock across the debounce window: the debounced save fires
	// and parks on its snapshot read lock, i.e. it is in flight.
	store.mu.Lock()
	time.Sleep(saveDebounce + debounceFireMargin)

	stopped := make(chan struct{})
	go func() {
		store.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		store.mu.Unlock()
		t.Fatal("Stop returned while a debounced registry save was still in flight")
	case <-time.After(stopBlockWindow):
	}
	store.mu.Unlock()

	select {
	case <-stopped:
	case <-time.After(persistDeadline):
		t.Fatalf("Stop did not return within %s of the in-flight save being released", persistDeadline)
	}

	// Everything stored before Stop is on disk the moment Stop returns.
	persisted, err := readPersistedRefs(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatalf("registry after Stop: %v", err)
	}
	if rec, ok := persisted[ref]; !ok || rec.Path != p {
		t.Fatalf("registry after Stop: entry for %s = %+v (present=%v), want path %q", ref, rec, ok, p)
	}

	// A stopped store never writes again, wherever OMNIPUS_HOME points next.
	laterDir := isolatedRegistryHome(t)
	later := createTempFile(t, laterDir, "after-stop.jpg")
	if _, err := store.Store(later, MediaMeta{CleanupPolicy: CleanupPolicyForgetOnly}, "scope-after-stop"); err != nil {
		t.Fatalf("Store after Stop: %v", err)
	}
	time.Sleep(saveDebounce + debounceFireMargin)
	if _, err := os.Stat(filepath.Join(laterDir, "registry.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stopped store wrote a registry into a later OMNIPUS_HOME (stat err = %v)", err)
	}

	if failures := registryFailures(); len(failures) > 0 {
		t.Errorf("%d registry save failure(s) logged; first: %s", len(failures), failures[0])
	}
}

// TestScheduledSave_MutationDuringInFlightSaveIsPersisted: a Store that lands
// after a debounced save has taken its snapshot, while that save is still
// writing, must still reach disk. The debounce callback cleared its "save
// pending" marker only AFTER writing, so scheduleSave saw a save still pending
// and skipped scheduling; the new ref stayed in memory only, and was gone after a
// restart unless some unrelated later mutation happened to save again.
func TestScheduledSave_MutationDuringInFlightSaveIsPersisted(t *testing.T) {
	registryFailures := captureRegistryFailures(t)
	dir := isolatedRegistryHome(t)
	store := NewFileMediaStore()
	t.Cleanup(store.Stop)

	first := createTempFile(t, dir, "first.jpg")
	second := createTempFile(t, dir, "second.jpg")
	forgetOnly := MediaMeta{CleanupPolicy: CleanupPolicyForgetOnly}

	if _, err := store.Store(first, forgetOnly, "scope-first"); err != nil {
		t.Fatalf("Store(first): %v", err)
	}

	// The debounced save for "first" fires and parks on its snapshot read lock.
	store.mu.Lock()
	time.Sleep(saveDebounce + debounceFireMargin)

	type storeResult struct {
		ref string
		err error
	}
	stored := make(chan storeResult, 1)
	go func() {
		ref, err := store.Store(second, forgetOnly, "scope-second")
		stored <- storeResult{ref: ref, err: err}
	}()
	// Let the second Store queue for the write lock behind the parked reader.
	// sync.RWMutex admits the parked reader first, so the snapshot is taken
	// before the second Store mutates, while that save is still in flight.
	time.Sleep(lockQueueMargin)
	store.mu.Unlock()

	res := <-stored
	if res.err != nil {
		t.Fatalf("Store(second): %v", res.err)
	}

	registry := filepath.Join(dir, "registry.json")
	deadline := time.Now().Add(persistDeadline)
	for {
		persisted, err := readPersistedRefs(registry)
		if err != nil {
			t.Fatalf("registry while waiting for the second save: %v", err)
		}
		if rec, ok := persisted[res.ref]; ok {
			if rec.Path != second {
				t.Fatalf("persisted path for %s = %q, want %q", res.ref, rec.Path, second)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ref %s, stored while a save was in flight, never reached %s within %s: no later save was scheduled",
				res.ref, registry, persistDeadline)
		}
		time.Sleep(persistPollEvery)
	}

	if failures := registryFailures(); len(failures) > 0 {
		t.Errorf("%d registry save failure(s) logged; first: %s", len(failures), failures[0])
	}
}
