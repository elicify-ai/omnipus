// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package entity implements the generic per-entity JSON file store that is
// Wave 1 (foundation) of ADR-054 ("Entity / config separation — per-entity
// files for agents", docs/internal/architecture/ADR-054-entity-config-separation.md).
//
// One file per entity, `<dir>/<id>.json`, written via
// fileutil.WriteFileAtomic (D2). Concurrency is layered exactly as D3
// decides: a 64-shard in-process striped mutex for fast same-process
// serialization, plus a sidecar lockfile (`<dir>/<id>.lock`, NEVER the data
// file itself — WriteFileAtomic replaces the data file by inode via rename,
// which would defeat a flock taken on it) flocked across the whole
// read-modify-write cycle for cross-process safety against OTHER
// COOPERATING writers. Neither lock stops a non-participating writer (jq,
// sed, shell redirection) — flock is advisory — and flock is a documented
// no-op on Windows (fileutil.WithFlock's platform files); this package makes
// no claim beyond what D3/D5 document.
//
// PLATFORM SCOPE (decided, not an open question): the cross-process half of
// this guarantee is POSIX-only. On Windows, WithFlock's no-op leaves ONLY
// the in-process striped mutex — two Windows processes racing
// Update/Create/Delete on the same entity are not protected from each
// other. This is proven both ways, not just asserted: store_crossprocess_test.go
// forks real OS processes to confirm the POSIX guarantee holds (and would
// fail if the sidecar flock were removed), and flock_isolation_test.go
// isolates the flock's own contribution from the striped mutex so that
// exact regression can never pass silently again. Both are `!windows`-gated
// — there is nothing POSIX-specific left to prove on Windows, since there
// the cross-process guarantee simply does not exist. See ADR-054 §5 and
// Store.Update's doc comment for the full rationale.
//
// Store[T] is generic over the entity payload type T itself (e.g. a future
// config.AgentConfig) — there is no envelope/wrapper struct, so the on-disk
// JSON shape is exactly T's own json tags. Because T is only ever seen as
// `any` by this package, it cannot call methods on T directly; callers
// supply small accessor closures (Accessors[T]) so the store can read/stamp
// the two fields its own contract needs: a stable ID and a creation
// timestamp for List's ordering (D2: sort by (created_at, id), no persisted
// sort_index — see Store.List's doc comment for why that design was
// withdrawn).
//
// This package does not itself decide what gets stored under entities/ —
// that wiring (config.AgentConfig, the registry-invalidation design, the
// REST conversion checklist) is later-wave work. Wave 1 is deliberately
// narrow: the store primitive, proven against parallel-write and
// same-entity-contention races, plus the filesystem carve-out
// (pkg/fspolicy) that keeps agent-writable code from ever reaching these
// files directly.
package entity

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when an entity ID does not exist on disk.
var ErrNotFound = errors.New("entity: not found")

// ErrAlreadyExists is returned by Create when the caller supplies an ID that
// already has a record on disk. Create never silently overwrites — an
// existing record must go through Update.
var ErrAlreadyExists = errors.New("entity: already exists")

// ErrInvalidID is returned when an entity ID is empty or contains path
// traversal characters (mirrors pkg/task's validateID — the same class of
// input, the same risk: an ID is used verbatim to build a filesystem path).
var ErrInvalidID = errors.New("entity: invalid id")

// validateID rejects IDs that are empty, contain path separators, contain
// "..", or contain a null byte — the same guard pkg/task's Store applies,
// since here too the ID is joined directly onto a directory to form a file
// path (Store.path / Store.lockPath).
func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: id must not be empty", ErrInvalidID)
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return fmt.Errorf("%w: invalid id %q", ErrInvalidID, id)
	}
	return nil
}

// Accessors lets Store[T] read and stamp the identity/creation-timestamp
// fields of an otherwise-opaque entity type T, without T implementing any
// interface. This keeps the on-disk JSON shape identical to T's own struct
// tags (no wrapper envelope) and means a future wire type such as
// config.AgentConfig needs no new methods to become storable this way — only
// a matching Accessors[T] value at the call site.
//
// All four fields are required; New panics if any is nil, since a Store with
// a partial Accessors set would fail confusingly deep inside Create/List
// rather than at construction time.
type Accessors[T any] struct {
	// GetID returns t's current identity, or "" if it has none yet (Create
	// then generates a UUID and calls SetID).
	GetID func(t *T) string
	// SetID stamps t's identity. Called by Create (new ID) and by Get/List
	// (backfilling an ID inferred from the filename, for records that
	// predate a store's identity convention — mirrors pkg/task's
	// `if t.ID == "" { t.ID = id }` on load).
	SetID func(t *T, id string)
	// GetCreatedAt returns t's creation timestamp, or the zero Time if never
	// set. Used by Create (to decide whether to stamp one) and by List (for
	// the (created_at, id) sort).
	GetCreatedAt func(t *T) time.Time
	// SetCreatedAt stamps t's creation timestamp. Called by Create only,
	// once, the first time the record is ever written.
	SetCreatedAt func(t *T, ts time.Time)
}

// requireComplete panics with a clear, immediate message if any accessor is
// nil, rather than letting a nil closure panic confusingly deep inside a
// later Create/Get/List call once real traffic is flowing.
func (a Accessors[T]) requireComplete() {
	if a.GetID == nil || a.SetID == nil || a.GetCreatedAt == nil || a.SetCreatedAt == nil {
		panic("entity: Accessors: GetID, SetID, GetCreatedAt, and SetCreatedAt are all required")
	}
}
