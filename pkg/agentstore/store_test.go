// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agentstore

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir())
}

// --- Write-then-verify (ADR-054 D6 corollary) ---

// TestCreate_WriteThenVerify_RoundTrip proves that a nil error from Create
// means the record is durably readable — the fix for the observed failure
// class where a caller trusted a nil error but the record was never actually
// on disk.
func TestCreate_WriteThenVerify_RoundTrip(t *testing.T) {
	s := newTestStore(t)

	cfg := &config.AgentConfig{Name: "Mia"}
	if err := s.Create("mia", cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if cfg.ID != "mia" {
		t.Fatalf("Create must stamp cfg.ID, got %q", cfg.ID)
	}
	if cfg.CreatedAt == nil {
		t.Fatal("Create must stamp CreatedAt")
	}

	got, err := s.Get("mia")
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if got.Name != "Mia" || got.ID != "mia" {
		t.Fatalf("Get returned %+v, want Name=Mia ID=mia", got)
	}

	// Also readable directly off disk, independent of this process's
	// in-memory state — proves durability, not just a cached return value.
	raw, err := os.ReadFile(filepath.Join(s.Dir(), "mia.json"))
	if err != nil {
		t.Fatalf("expected mia.json on disk: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("mia.json is empty")
	}
}

// TestCreate_DuplicateID_Rejected verifies Create never silently overwrites
// an existing record — a second Create for the same id must fail, and the
// original record must survive unchanged.
func TestCreate_DuplicateID_Rejected(t *testing.T) {
	s := newTestStore(t)

	if err := s.Create("mia", &config.AgentConfig{Name: "Mia"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	err := s.Create("mia", &config.AgentConfig{Name: "Impostor"})
	if err == nil {
		t.Fatal("expected an error creating a duplicate id, got nil")
	}
	if !errors.Is(err, entity.ErrAlreadyExists) {
		t.Errorf("expected errors.Is(err, entity.ErrAlreadyExists), got %v", err)
	}

	got, getErr := s.Get("mia")
	if getErr != nil {
		t.Fatalf("Get after rejected duplicate create: %v", getErr)
	}
	if got.Name != "Mia" {
		t.Fatalf("original record was overwritten: got Name=%q, want Mia", got.Name)
	}
}

// TestCreate_IDMismatch_Rejected verifies a caller-supplied cfg.ID that
// disagrees with the requested id is rejected rather than silently
// overridden (defense against a reused-request-struct caller bug).
//
// Anti-shortcut: Store.Create's id/cfg.ID mismatch check
// (pkg/agentstore/store.go) returns a bare fmt.Errorf with no sentinel of
// its own — it does not wrap entity.ErrInvalidID, entity.ErrAlreadyExists,
// or any write/verify-read-back error, but all of those ALSO produce a
// non-nil err from Create. A bare `err == nil` check would keep passing if
// a regression accidentally routed this case through one of those other
// failure paths instead of the intended mismatch rejection (e.g. a reused
// id from a leaked temp dir hitting ErrAlreadyExists, or a validateID
// regression flagging "someone-else" as ErrInvalidID) — so assert the exact
// rejection reason via its distinctive message, rule out the sentinel'd
// alternatives via errors.Is, and confirm no record was persisted under
// either id (ruling out a write succeeding under the wrong ID).
func TestCreate_IDMismatch_Rejected(t *testing.T) {
	s := newTestStore(t)
	err := s.Create("mia", &config.AgentConfig{ID: "someone-else", Name: "Mia"})
	if err == nil {
		t.Fatal("expected an error for a cfg.ID/id mismatch, got nil")
	}
	if !strings.Contains(err.Error(), `cfg.ID "someone-else" does not match requested id`) {
		t.Errorf("expected the id-mismatch message naming both ids, got: %v", err)
	}
	if errors.Is(err, entity.ErrInvalidID) {
		t.Error("rejection must be the id/cfg.ID mismatch check, not ErrInvalidID")
	}
	if errors.Is(err, entity.ErrAlreadyExists) {
		t.Error("rejection must be the id/cfg.ID mismatch check, not ErrAlreadyExists")
	}

	// No side effect: a rejected mismatched create must not have persisted
	// anything under either id.
	if _, getErr := s.Get("mia"); !errors.Is(getErr, entity.ErrNotFound) {
		t.Errorf("mismatched create must not persist a record under %q, Get error = %v", "mia", getErr)
	}
	if _, getErr := s.Get("someone-else"); !errors.Is(getErr, entity.ErrNotFound) {
		t.Errorf("mismatched create must not persist a record under %q, Get error = %v", "someone-else", getErr)
	}
}

// TestCreate_ConcurrentDifferentAgents_AllPersist is the direct regression
// test for the bug that motivated this ADR: N concurrent creates of N
// DIFFERENT agents must all succeed AND all be durably present afterward —
// no lost writes from lock convoy or a shared-file race. Run with -race.
func TestCreate_ConcurrentDifferentAgents_AllPersist(t *testing.T) {
	s := newTestStore(t)

	const n = 8
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "agent-" + string(rune('a'+i))
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			errs[i] = s.Create(id, &config.AgentConfig{Name: id})
		}(i, id)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Create(%q) failed: %v", ids[i], err)
		}
	}

	list, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("List skipped = %v, want none", skipped)
	}
	gotIDs := make([]string, 0, len(list))
	for _, a := range list {
		gotIDs = append(gotIDs, a.ID)
	}
	sort.Strings(gotIDs)
	wantIDs := append([]string(nil), ids...)
	sort.Strings(wantIDs)
	if len(gotIDs) != n {
		t.Fatalf("List returned %d agents, want %d: got=%v want=%v", len(gotIDs), n, gotIDs, wantIDs)
	}
	for i := range gotIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("List = %v, want %v (some concurrent create was lost)", gotIDs, wantIDs)
		}
	}
}

// --- Notifier wiring (ADR-054 §5 registry invalidation) ---

type fakeNotifier struct {
	mu       sync.Mutex
	upserted map[string]*config.AgentConfig
	deleted  map[string]bool
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{upserted: map[string]*config.AgentConfig{}, deleted: map[string]bool{}}
}

func (f *fakeNotifier) AgentUpserted(id string, cfg *config.AgentConfig) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted[id] = cfg
}

func (f *fakeNotifier) AgentDeleted(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted[id] = true
}

func TestNotifier_CalledOnCreateUpdateDelete(t *testing.T) {
	s := newTestStore(t)
	n := newFakeNotifier()
	s.SetNotifier(n)

	if err := s.Create("mia", &config.AgentConfig{Name: "Mia"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	n.mu.Lock()
	_, upserted := n.upserted["mia"]
	n.mu.Unlock()
	if !upserted {
		t.Fatal("expected AgentUpserted to be called after Create")
	}

	if _, err := s.Update("mia", func(a *config.AgentConfig) error {
		a.Name = "Mia2"
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	n.mu.Lock()
	got := n.upserted["mia"]
	n.mu.Unlock()
	if got == nil || got.Name != "Mia2" {
		t.Fatalf("expected AgentUpserted to reflect the update, got %+v", got)
	}

	if err := s.Delete("mia"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	n.mu.Lock()
	deleted := n.deleted["mia"]
	n.mu.Unlock()
	if !deleted {
		t.Fatal("expected AgentDeleted to be called after Delete")
	}
}

// TestSetNotifier_NilIsSafe verifies a Store with no notifier wired (the
// zero-value default) still performs Create/Update/Delete correctly — no nil
// panic on the notifier path.
func TestSetNotifier_NilIsSafe(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create("mia", &config.AgentConfig{Name: "Mia"}); err != nil {
		t.Fatalf("Create without notifier: %v", err)
	}
	if _, err := s.Update("mia", func(a *config.AgentConfig) error { return nil }); err != nil {
		t.Fatalf("Update without notifier: %v", err)
	}
	if err := s.Delete("mia"); err != nil {
		t.Fatalf("Delete without notifier: %v", err)
	}
}

// --- Skip semantics (ADR-054 D7 / D6 rule 2) ---

// TestList_SkippedDistinguishesLoadFailureFromNeverExisted writes one valid
// record, one malformed record directly to disk (bypassing Create — this
// simulates a hand-corrupted or partially-written file), and confirms List
// reports the malformed one in `skipped`, NOT in the result slice, and that a
// third id that was simply never created is absent from both — the
// distinction D6 rule 2 / D7 require so callers can tell "this entity is
// broken" apart from "this entity never existed".
func TestList_SkippedDistinguishesLoadFailureFromNeverExisted(t *testing.T) {
	s := newTestStore(t)

	if err := s.Create("good", &config.AgentConfig{Name: "Good"}); err != nil {
		t.Fatalf("Create good: %v", err)
	}
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "broken.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write broken.json: %v", err)
	}

	list, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != "good" {
		t.Fatalf("List result = %+v, want exactly [good]", list)
	}
	if len(skipped) != 1 || skipped[0] != "broken" {
		t.Fatalf("List skipped = %v, want exactly [broken]", skipped)
	}

	// "never-existed" is absent from both, and Get on it fails not-found.
	for _, a := range list {
		if a.ID == "never-existed" {
			t.Fatal("never-existed must not appear in the result list")
		}
	}
	for _, id := range skipped {
		if id == "never-existed" {
			t.Fatal("never-existed must not appear in skipped")
		}
	}
	if _, err := s.Get("never-existed"); err == nil {
		t.Fatal("expected Get(never-existed) to fail")
	} else if !errors.Is(err, entity.ErrNotFound) {
		t.Errorf("expected errors.Is(err, entity.ErrNotFound), got %v", err)
	}
}

// TestDelete_NotFound verifies deleting a never-existing id surfaces a clear
// not-found error rather than succeeding silently.
func TestDelete_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.Delete("does-not-exist")
	if err == nil {
		t.Fatal("expected an error deleting a non-existent agent")
	}
	if !errors.Is(err, entity.ErrNotFound) {
		t.Errorf("expected errors.Is(err, entity.ErrNotFound), got %v", err)
	}
}
