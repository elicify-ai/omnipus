// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package entity

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Store is a generic per-entity JSON file store rooted at a single
// directory. See the package doc for the full concurrency/locking contract
// (ADR-054 D2/D3).
type Store[T any] struct {
	dir string
	acc Accessors[T]
}

// New constructs a Store[T] rooted at dir. Callers should construct exactly
// one Store per (T, dir) pair and share it — e.g. one package-level agent
// entity store for entities/agents, one for entities/channels — since the
// process-wide fileLock is keyed on the full file path, not on any
// per-instance state, so this is a documentation contract for callers'
// sanity, not a correctness requirement of the lock itself.
//
// New panics if acc is missing any accessor field — a Store misconfigured
// this way would otherwise fail confusingly deep inside a later Create/List
// call once real traffic is already flowing.
func New[T any](dir string, acc Accessors[T]) *Store[T] {
	acc.requireComplete()
	return &Store[T]{dir: dir, acc: acc}
}

// Dir returns the store's entity directory.
func (s *Store[T]) Dir() string { return s.dir }

// path returns the absolute path for an entity's data file.
func (s *Store[T]) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// lockPath returns the sidecar lockfile path for id.
//
// INVARIANT (ADR-054 D3, "Sidecar lifecycle"): this file is created lazily
// (fileutil.WithFlock opens it O_RDWR|O_CREATE) and is NEVER removed by this
// package — not on Delete, not anywhere else, for any reason, including full
// entity deletion. WithFlock's O_CREATE means a deleted-and-recreated lock
// file would get a fresh inode; two processes could then each successfully
// flock what they believe is "the" lock for this entity ID while actually
// holding two distinct inodes' locks, silently losing the mutual-exclusion
// guarantee the whole sidecar design exists to provide. Keeping the lockfile
// immortal for the lifetime of the STORE DIRECTORY (not the entity) is what
// keeps its identity — and therefore its lock — stable across a
// delete-then-recreate of the same id. Do not add cleanup code for these
// files without re-deriving this invariant from scratch.
func (s *Store[T]) lockPath(id string) string {
	return filepath.Join(s.dir, id+".lock")
}

// load reads and parses a single entity file. It is a bare read with no
// locking of its own (mirrors pkg/task.Store.load) — a lone Get/List read is
// allowed to observe a slightly stale (but never torn, since
// WriteFileAtomic's rename is atomic) file; it is the read-MODIFY-write
// cycle in Update/Create that needs the lock, not a standalone read. This
// deliberately avoids taking a LOCK_EX sidecar flock on every read (D3,
// "READ PATH" — fileutil.WithFlock has no shared-lock mode, so flocking
// every read would serialize concurrent readers against each other for no
// correctness benefit here).
func (s *Store[T]) load(id string) (*T, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("entity: read %q: %w", id, err)
	}
	var t T
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("entity: parse %q: %w", id, err)
	}
	if s.acc.GetID(&t) == "" {
		s.acc.SetID(&t, id)
	}
	return &t, nil
}

// write marshals and atomically persists t under id. It performs no locking
// of its own — callers hold the striped mutex + sidecar flock around the
// whole read-modify-write cycle they are part of.
func (s *Store[T]) write(id string, t *T) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("entity: create dir: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("entity: marshal %q: %w", id, err)
	}
	return fileutil.WriteFileAtomic(s.path(id), data, 0o600)
}

// Create persists a new entity. It generates a UUID when the entity has no
// ID yet, stamps CreatedAt (via SetCreatedAt) unless the caller already set
// one, and rejects an ID that already has a record on disk (ErrAlreadyExists
// — Create never silently overwrites).
//
// The whole existence-check + write is performed under BOTH the in-process
// striped mutex (fast path, same process) and the cross-process sidecar
// lockfile (D3), and — per D6's "write-then-verify" corollary — Create reads
// the just-written file back and confirms it parses with the expected ID
// before reporting success. This is the durable fix for the ADR's motivating
// failure class: a roster that names an entity which was never actually
// persisted.
func (s *Store[T]) Create(t *T) error {
	id := s.acc.GetID(t)
	if id == "" {
		id = uuid.New().String()
		s.acc.SetID(t, id)
	}
	if err := validateID(id); err != nil {
		return err
	}
	if s.acc.GetCreatedAt(t).IsZero() {
		s.acc.SetCreatedAt(t, time.Now().UTC())
	}

	mu := fileLock.get(s.path(id))
	mu.Lock()
	defer mu.Unlock()

	return fileutil.WithFlock(s.lockPath(id), func() error {
		if _, statErr := os.Stat(s.path(id)); statErr == nil {
			return fmt.Errorf("entity: create %q: %w", id, ErrAlreadyExists)
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("entity: create %q: stat: %w", id, statErr)
		}

		if err := s.write(id, t); err != nil {
			return err
		}

		// Write-then-verify (ADR-054 D6 corollary).
		verify, err := s.load(id)
		if err != nil {
			return fmt.Errorf("entity: create %q: verify read-back: %w", id, err)
		}
		if got := s.acc.GetID(verify); got != id {
			return fmt.Errorf("entity: create %q: verify read-back: id mismatch, got %q", id, got)
		}
		return nil
	})
}

// Get returns the entity with the given id, or ErrNotFound if absent.
func (s *Store[T]) Get(id string) (*T, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.load(id)
}

// Exists reports whether an entity with id exists on disk.
func (s *Store[T]) Exists(id string) bool {
	if validateID(id) != nil {
		return false
	}
	_, err := os.Stat(s.path(id))
	return err == nil
}

// scanIDs returns every entity ID present in the store directory, derived
// from each regular *.json file's name. Sidecar *.lock files never match
// this suffix, so they are excluded without any special-casing. It returns
// the raw os.ReadDir error as-is (including a not-exist error) so List keeps
// its own missing-dir handling.
func (s *Store[T]) scanIDs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	return ids, nil
}

// List returns every entity in the store, sorted by (created_at, id)
// ascending, plus the IDs of any files that existed but failed to load.
//
// Ordering (ADR-054 D2): explicit os.ReadDir + sort, never directory order.
// Sorting by (created_at, id) rather than a persisted sort_index is a
// deliberate ADR decision — a persisted index would need a read-all-then-
// write on every Create (max(existing)+1), re-serializing exactly the
// operation this store exists to parallelize, and would be an unowned
// global invariant. A monotonic creation timestamp has no such allocation
// race; id is the tiebreak for two entities created in the same instant.
//
// Skip semantics (D7 / this wave's brief): a single unparseable entity file
// must never fail the whole listing. Each such ID is logged at Warn and
// returned in skipped rather than in the result slice, so a caller can tell
// "this ID exists but failed to load" (present in skipped) apart from "this
// ID never existed" (absent from both) — the distinction D7 requires so
// routing/delegation can refuse rather than silently re-route or inherit a
// permissive fallback identity for a skipped record.
func (s *Store[T]) List() (result []T, skipped []string, err error) {
	ids, err := s.scanIDs()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("entity: list dir %q: %w", s.dir, err)
	}

	result = make([]T, 0, len(ids))
	for _, id := range ids {
		t, loadErr := s.load(id)
		if loadErr != nil {
			slog.Warn("entity: skip unreadable entity file", "dir", s.dir, "id", id, "error", loadErr)
			skipped = append(skipped, id)
			continue
		}
		result = append(result, *t)
	}

	sort.Slice(result, func(i, j int) bool {
		ci, cj := s.acc.GetCreatedAt(&result[i]), s.acc.GetCreatedAt(&result[j])
		if !ci.Equal(cj) {
			return ci.Before(cj)
		}
		return s.acc.GetID(&result[i]) < s.acc.GetID(&result[j])
	})
	return result, skipped, nil
}

// Update loads the entity identified by id, applies mutate to it, and
// persists the result — all under the SAME striped-mutex + sidecar-flock
// acquisition, closing the cross-process read-modify-write race D3
// describes. mutate must not change the entity's ID (SetID is not
// re-validated after mutate runs); it returning a non-nil error aborts the
// whole cycle with nothing written.
func (s *Store[T]) Update(id string, mutate func(*T) error) (*T, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}

	mu := fileLock.get(s.path(id))
	mu.Lock()
	defer mu.Unlock()

	var result *T
	err := fileutil.WithFlock(s.lockPath(id), func() error {
		t, loadErr := s.load(id)
		if loadErr != nil {
			return loadErr
		}
		if mutateErr := mutate(t); mutateErr != nil {
			return mutateErr
		}
		if writeErr := s.write(id, t); writeErr != nil {
			return writeErr
		}
		result = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Delete removes the entity's data file. Per the lockPath invariant above,
// the sidecar lockfile itself is NEVER removed here.
func (s *Store[T]) Delete(id string) error {
	if err := validateID(id); err != nil {
		return err
	}

	mu := fileLock.get(s.path(id))
	mu.Lock()
	defer mu.Unlock()

	return fileutil.WithFlock(s.lockPath(id), func() error {
		if err := os.Remove(s.path(id)); err != nil {
			if os.IsNotExist(err) {
				return ErrNotFound
			}
			return fmt.Errorf("entity: delete %q: %w", id, err)
		}
		return nil
	})
}
