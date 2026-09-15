// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/entity"
)

// goalsSubdir is the fixed directory segment under $OMNIPUS_HOME/entities/
// this store is rooted at (ADR-054 D2 precedent, mirrored by GOAL-FR-001).
const goalsSubdir = "goals"

// accessors wires Goal's identity/creation-timestamp fields into the shape
// entity.Store[Goal] needs — see pkg/entity.Accessors' doc comment.
var accessors = entity.Accessors[Goal]{
	GetID:        func(g *Goal) string { return g.GoalID },
	SetID:        func(g *Goal, id string) { g.GoalID = id },
	GetCreatedAt: func(g *Goal) time.Time { return g.CreatedAt },
	SetCreatedAt: func(g *Goal, ts time.Time) { g.CreatedAt = ts },
}

// goalLockAcquireFn/goalLockReleaseFn are swappable observation hooks fired
// immediately before and after each Store Create/Update/Delete call —
// mirroring pkg/session's FR-101 sessionLockAcquireFn/sessionLockReleaseFn
// pattern (R-33/C-09/GOAL-FR-008's verification half). Production code
// never swaps these (the zero-value no-ops); tests do, to make the
// otherwise-invisible pkg/entity lock acquisition observable without a
// race-detector-defeating sleep — see lock_test.go.
//
// LOCK ORDER (R-33, binding across the whole delivery):
//
//	goalLock -> taskFileLock -> sessionLock -> cacheMu
//
// "goalLock" is what these two hooks bracket. A caller that already holds
// an ADR-057 session shard (pkg/session/unified_lock.go's sessionLock(id))
// or its cacheMu MUST NOT call into this store while holding it — the goal
// lock must be acquired and released strictly BEFORE a session lock is
// taken, never nested inside one. (go test -race is not a lock-order
// checker — it reports nothing for an inversion that does not happen to
// deadlock in the run under test, which is precisely why this seam exists
// instead of relying on -race alone.)
var (
	goalLockAcquireFn = func(id string) {}
	goalLockReleaseFn = func(id string) {}
)

// Store is the goal-specific wrapper over the generic entity store
// (pkg/entity, reused as-is — see doc.go). It supplies Goal-shaped
// Accessors wiring plus the goal-specific query surface (predicate.go) a
// generic store has no way to know about.
type Store struct {
	inner *entity.Store[Goal]
}

// NewStore creates a Store rooted at $OMNIPUS_HOME/entities/goals.
// omnipusHome is $OMNIPUS_HOME itself (e.g. config.OmnipusHomeDir()) — this
// constructor appends the fixed "entities/goals" path segment. Named
// NewStore rather than New because goal.New already names the Goal record
// constructor (goal.go) — this package's most-reached-for symbol is the
// domain object, not its store.
//
// Eagerly (best-effort) creates the directory, mirroring pkg/agentstore.New:
// entity.Store[T].Create takes its sidecar flock before its own directory
// MkdirAll runs (inside write(), which executes INSIDE that same flock
// callback), so on a truly fresh install the very first Create would
// otherwise fail outright with "no such file or directory" opening the lock
// file. Pre-creating the directory here, at Store construction — which
// every boot path calls once, well before any concurrent Create — sidesteps
// that for goals the same way pkg/agentstore already does for agents.
func NewStore(omnipusHome string) *Store {
	dir := filepath.Join(omnipusHome, "entities", goalsSubdir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("goal: could not pre-create entity directory (Create will retry via entity.Store's own MkdirAll)",
			"dir", dir, "error", err)
	}
	return &Store{inner: entity.New[Goal](dir, accessors)}
}

// Dir returns the store's entity directory ($OMNIPUS_HOME/entities/goals).
func (s *Store) Dir() string { return s.inner.Dir() }

// Get returns the goal record for id. Returns an error wrapping
// entity.ErrNotFound when absent (errors.Is(err, entity.ErrNotFound) still
// works through this wrapper, per fmt.Errorf's %w).
func (s *Store) Get(id string) (*Goal, error) {
	g, err := s.inner.Get(id)
	if err != nil {
		return nil, fmt.Errorf("goal: get %q: %w", id, err)
	}
	return g, nil
}

// List returns every parseable goal record, sorted by (created_at, id) —
// entity.Store.List's ordering contract — plus the ids of any records that
// exist on disk but failed to load (each already logged at Warn by
// entity.Store.List itself).
func (s *Store) List() (goals []Goal, skipped []string, err error) {
	goals, skipped, err = s.inner.List()
	if err != nil {
		return nil, nil, fmt.Errorf("goal: list: %w", err)
	}
	return goals, skipped, nil
}

// ErrOwnerAlreadyHasGoal is returned by Create when g.OwnerKind is task and
// the owning task already has a goal record — R-04's "one goal per task for
// the task's whole life" invariant. A task that already has a goal (in any
// phase — defining, active, or terminal) must go through Reactivate on its
// EXISTING record when its task re-runs, never through a second Create.
var ErrOwnerAlreadyHasGoal = errors.New("goal: owner already has a goal record")

// Create persists a new goal record. It validates g (Goal.Validate), then —
// for a task-owned goal only — checks R-04's one-goal-per-task invariant
// before writing: a second Create for a task that already has a goal record
// (in ANY phase) is refused with ErrOwnerAlreadyHasGoal, because a task
// re-run must re-enter the EXISTING record via Reactivate, never mint a
// second one. A session-owned goal has no such uniqueness constraint (a
// session may accumulate several terminal goal records across its life,
// one per /goal invocation, ADR-081 D1).
//
// entity.Store.Create already performs write-then-verify internally
// (ADR-054 D6 corollary) — this wrapper does not duplicate that check; a
// nil error means the record is confirmed durable on disk.
//
// Create mints g.GoalID itself (when the caller left it empty) BEFORE
// calling into entity.Store.Create, rather than letting entity.Store.Create
// mint it internally the way pkg/entity's own doc comment describes for a
// bare Store[T]. This keeps the lock-observation seam
// (goalLockAcquireFn/goalLockReleaseFn) meaningful: those hooks fire keyed
// on g.GoalID, and a caller that read g.GoalID back out after a Create call
// racing this one's acquire/release pair must see the SAME id the hooks
// reported, never the pre-assignment empty string one path and the final
// UUID on the other.
func (s *Store) Create(g *Goal) error {
	if g == nil {
		return fmt.Errorf("goal: create: nil goal")
	}
	if g.GoalID == "" {
		g.GoalID = uuid.New().String()
	}
	if err := g.Validate(); err != nil {
		return fmt.Errorf("goal: create: %w", err)
	}
	if g.OwnerKind == generated.GoalOwnerKindTask {
		existing, err := s.GetByOwner(g.OwnerKind, g.OwnerID)
		if err != nil && !errors.Is(err, ErrOwnerNotFound) {
			return fmt.Errorf("goal: create: check existing owner: %w", err)
		}
		if existing != nil {
			return fmt.Errorf("goal: create: %w: task %q already has goal %q",
				ErrOwnerAlreadyHasGoal, g.OwnerID, existing.GoalID)
		}
	}

	id := g.GoalID
	goalLockAcquireFn(id)
	defer goalLockReleaseFn(id)
	if err := s.inner.Create(g); err != nil {
		return fmt.Errorf("goal: create: %w", err)
	}
	return nil
}

// Update performs a read-modify-write of the goal identified by id under
// pkg/entity's striped-mutex + sidecar-flock guard, via the mutate closure.
// mutate must not change the record's GoalID.
//
// This is the single-writer update path C-09 asks for: every engine wave
// that changes goal state (RecordClaim, RecordVerdict, RecordAttempt,
// Activate, Terminate, Reactivate, SetCriteria/SetDoD/SupersedeCriteria) is
// expected to call it through Store.Update rather than writing the on-disk
// file directly, so the oracle "the goal record's attempts-used counter,
// read back from pkg/goal's store after the call, is unchanged" has one
// observable: read the record back through Get/List after the call, not a
// cached in-memory value an implementation might never have persisted.
func (s *Store) Update(id string, mutate func(*Goal) error) (*Goal, error) {
	goalLockAcquireFn(id)
	defer goalLockReleaseFn(id)
	updated, err := s.inner.Update(id, mutate)
	if err != nil {
		return nil, fmt.Errorf("goal: update %q: %w", id, err)
	}
	return updated, nil
}

// Delete removes the goal record identified by id. Per D9/D14, the only
// legitimate caller of this in production is retention (S3) or the
// deletion of the record's owner — Delete is never how a goal ENDS (that is
// Terminate, a status transition on a retained record); this method exists
// for that later, narrower purpose and for test cleanup.
func (s *Store) Delete(id string) error {
	goalLockAcquireFn(id)
	defer goalLockReleaseFn(id)
	if err := s.inner.Delete(id); err != nil {
		return fmt.Errorf("goal: delete %q: %w", id, err)
	}
	return nil
}
