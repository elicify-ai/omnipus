// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package entity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// testEntity is the small stand-in payload used throughout this suite — it
// exercises the exact Accessors[T] contract a future config.AgentConfig
// wiring would use, without depending on pkg/config from pkg/entity (this
// package must stay generic and dependency-free of any concrete entity
// type).
type testEntity struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func testAccessors() Accessors[testEntity] {
	return Accessors[testEntity]{
		GetID:        func(t *testEntity) string { return t.ID },
		SetID:        func(t *testEntity, id string) { t.ID = id },
		GetCreatedAt: func(t *testEntity) time.Time { return t.CreatedAt },
		SetCreatedAt: func(t *testEntity, ts time.Time) { t.CreatedAt = ts },
	}
}

func newTestStore(t *testing.T) *Store[testEntity] {
	t.Helper()
	dir := t.TempDir()
	return New[testEntity](dir, testAccessors())
}

// --- Round-trip: create -> get -> list -> update -> delete ---

func TestRoundTrip_CreateGetListUpdateDelete(t *testing.T) {
	s := newTestStore(t)

	e := &testEntity{Name: "alpha"}
	if err := s.Create(e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.ID == "" {
		t.Fatal("Create must stamp an ID when the caller supplied none")
	}
	if e.CreatedAt.IsZero() {
		t.Fatal("Create must stamp CreatedAt when the caller supplied none")
	}

	got, err := s.Get(e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "alpha" || got.ID != e.ID {
		t.Fatalf("Get returned %+v, want Name=alpha ID=%s", got, e.ID)
	}

	list, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("List skipped = %v, want none", skipped)
	}
	if len(list) != 1 || list[0].ID != e.ID {
		t.Fatalf("List = %+v, want exactly the created entity", list)
	}

	updated, err := s.Update(e.ID, func(t *testEntity) error {
		t.Name = "beta"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "beta" {
		t.Fatalf("Update result Name = %q, want beta", updated.Name)
	}

	reread, err := s.Get(e.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if reread.Name != "beta" {
		t.Fatalf("Get after Update Name = %q, want beta (update did not persist)", reread.Name)
	}

	if err := s.Delete(e.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	if s.Exists(e.ID) {
		t.Fatal("Exists after Delete = true, want false")
	}

	// The sidecar lockfile must survive the delete (ADR-054 §5 invariant):
	// deleting it would let a recreated one mint a fresh inode.
	if _, err := os.Stat(s.lockPath(e.ID)); err != nil {
		t.Fatalf("sidecar lockfile missing after Delete: %v (must NEVER be removed)", err)
	}
}

func TestCreate_RejectsDuplicateID(t *testing.T) {
	s := newTestStore(t)
	e := &testEntity{ID: "fixed-id", Name: "first"}
	if err := s.Create(e); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	dup := &testEntity{ID: "fixed-id", Name: "second"}
	if err := s.Create(dup); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second Create with same ID = %v, want ErrAlreadyExists", err)
	}
	// The original record must be unaffected.
	got, err := s.Get("fixed-id")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "first" {
		t.Fatalf("Get after rejected duplicate Create = %q, want %q (must not be overwritten)", got.Name, "first")
	}
}

func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get("does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestList_EmptyDirNoError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	s := New[testEntity](dir, testAccessors())
	list, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List on nonexistent dir: %v", err)
	}
	if len(list) != 0 || len(skipped) != 0 {
		t.Fatalf("List on nonexistent dir = (%v, %v), want (empty, empty)", list, skipped)
	}
}

func TestList_SortedByCreatedAtThenID(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Two entities sharing the SAME created_at must tiebreak on id.
	mustCreate := func(id string, ts time.Time) {
		e := &testEntity{ID: id, CreatedAt: ts}
		if err := s.Create(e); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	mustCreate("z-first-created", base)
	mustCreate("a-same-time", base)
	mustCreate("m-created-later", base.Add(time.Hour))

	list, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("List skipped = %v, want none", skipped)
	}
	wantOrder := []string{"a-same-time", "z-first-created", "m-created-later"}
	if len(list) != len(wantOrder) {
		t.Fatalf("List returned %d entities, want %d", len(list), len(wantOrder))
	}
	for i, want := range wantOrder {
		if list[i].ID != want {
			t.Errorf("List[%d].ID = %q, want %q (full order: %v)", i, list[i].ID, want, idsOf(list))
		}
	}
}

func idsOf(list []testEntity) []string {
	ids := make([]string, len(list))
	for i, e := range list {
		ids[i] = e.ID
	}
	return ids
}

// --- Corrupt-file handling: one bad file must not fail the whole List ---

func TestList_SkipsCorruptFile_DoesNotError(t *testing.T) {
	s := newTestStore(t)

	good := &testEntity{Name: "good"}
	if err := s.Create(good); err != nil {
		t.Fatalf("Create good: %v", err)
	}

	corruptPath := filepath.Join(s.Dir(), "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	list, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List must not error on a corrupt file, got: %v", err)
	}
	if len(list) != 1 || list[0].ID != good.ID {
		t.Fatalf("List = %+v, want exactly the good entity", list)
	}
	if len(skipped) != 1 || skipped[0] != "corrupt" {
		t.Fatalf("List skipped = %v, want [\"corrupt\"]", skipped)
	}

	// The skipped ID must be distinguishable from an ID that never existed:
	// Get on the corrupt record surfaces the parse error rather than
	// ErrNotFound, and it must NOT silently be treated as absent.
	if _, err := s.Get("corrupt"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(\"corrupt\") = %v, want a parse error (not ErrNotFound — the file exists)", err)
	}
	if _, err := s.Get("truly-never-existed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(never-existed) = %v, want ErrNotFound", err)
	}
}

// --- Parallel-write: N goroutines creating DIFFERENT entities concurrently ---
// This is the ADR's whole point: distinct entities must proceed fully in
// parallel and none may be lost. Run with -race.

func TestParallelWrite_DifferentEntities_AllPersist(t *testing.T) {
	s := newTestStore(t)
	const n = 100

	var wg sync.WaitGroup
	errs := make([]error, n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := &testEntity{ID: fmt.Sprintf("entity-%03d", i), Name: fmt.Sprintf("name-%d", i)}
			errs[i] = s.Create(e)
			ids[i] = e.ID
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Create(entity-%03d) failed: %v", i, err)
		}
	}

	list, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("List skipped = %v, want none", skipped)
	}
	if len(list) != n {
		t.Fatalf("List returned %d entities, want %d — some concurrent creates were lost", len(list), n)
	}
	seen := make(map[string]bool, n)
	for _, e := range list {
		seen[e.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("entity %q missing from List after concurrent Create", id)
		}
	}
}

// --- Same-entity contention: N goroutines updating the SAME entity must
// serialize correctly with no lost update ---

func TestSameEntityContention_ConcurrentUpdatesSerializeNoLostUpdate(t *testing.T) {
	s := newTestStore(t)
	e := &testEntity{ID: "counter", Name: "0"}
	if err := s.Create(e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const n = 200
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Update("counter", func(t *testEntity) error {
				// Read-modify-write on an embedded counter encoded in Name.
				cur := 0
				_, scanErr := fmt.Sscanf(t.Name, "%d", &cur)
				if scanErr != nil && t.Name != "0" {
					return fmt.Errorf("parse counter %q: %w", t.Name, scanErr)
				}
				cur++
				t.Name = fmt.Sprintf("%d", cur)
				return nil
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Update #%d failed: %v", i, err)
		}
	}

	final, err := s.Get("counter")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := fmt.Sprintf("%d", n)
	if final.Name != want {
		t.Fatalf("final counter = %q, want %q (a lost update occurred: %d increments over %d goroutines)",
			final.Name, want, n, n)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Update("missing", func(t *testEntity) error { return nil })
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) = %v, want ErrNotFound", err)
	}
}

func TestUpdate_MutateErrorAbortsWithoutWriting(t *testing.T) {
	s := newTestStore(t)
	e := &testEntity{Name: "unchanged"}
	if err := s.Create(e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	sentinel := errors.New("mutate refused")
	_, err := s.Update(e.ID, func(t *testEntity) error {
		t.Name = "should-not-persist"
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Update error = %v, want sentinel", err)
	}
	got, getErr := s.Get(e.ID)
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if got.Name != "unchanged" {
		t.Fatalf("Get.Name = %q after aborted mutate, want %q (mutate error must not persist)", got.Name, "unchanged")
	}
}

func TestDelete_NotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.Delete("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete(missing) = %v, want ErrNotFound", err)
	}
}

func TestValidateID_RejectsTraversal(t *testing.T) {
	s := newTestStore(t)
	// "" is deliberately excluded from the Create case: an empty ID at
	// Create time means "auto-generate a UUID", which is valid input, not a
	// traversal attempt. Get/Delete/Update have no such auto-generation
	// escape hatch, so "" must be rejected there.
	for _, bad := range []string{"../escape", "a/b", "a\\b", "a..b/c"} {
		if err := s.Create(&testEntity{ID: bad}); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Create(id=%q) = %v, want ErrInvalidID", bad, err)
		}
	}
	for _, bad := range []string{"", "../escape", "a/b", "a\\b", "a..b/c"} {
		if _, err := s.Get(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Get(%q) = %v, want ErrInvalidID", bad, err)
		}
	}
}

func TestNew_PanicsOnIncompleteAccessors(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("New with incomplete Accessors must panic")
		}
	}()
	New[testEntity](t.TempDir(), Accessors[testEntity]{})
}
