// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 W6, FR-020 — the LifecycleStore's secondary parent index.
//
// US-4 ("the parent→child edge is durable and queryable") needs "children of
// X" to cost one file read per child, never a scan of every persisted
// session_id (pkg/session/lifecycle.go's pre-ADR-057 List did exactly that —
// scanSessionIDs() + a full Load() per id, unconditionally). This file adds
// the missing index: an in-memory map from a session's ParentDurableKey (its
// DIRECT parent's own live routing/session id, D1 — see LifecycleRecord's own
// field doc in lifecycle.go) to the set of session_ids whose own
// ParentDurableKey equals that value.
//
// The index is a property of one LifecycleStore instance (like its striped
// lock), not a package-level global — two LifecycleStore values rooted at
// different directories must never share index state.
package session

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
)

// lifecycleParentIndex maps a durable ParentDurableKey to the set of
// session_ids that are its DIRECT children (FR-020). Safe for concurrent
// use: a private sync.RWMutex guards byParent, independent of
// LifecycleStore's per-session striped lock — an update to this shared map
// can be triggered from any of the 64 session shards concurrently, so it
// needs its own synchronization regardless of which shard's Persist call is
// driving it.
//
// Lock ordering: callers that already hold a LifecycleStore per-session
// shard lock (persistLocked, pruneTerminalOne) may safely also take this
// mutex — the reverse (holding this mutex while trying to acquire a
// per-session shard lock) never happens anywhere in this package, so there
// is no cycle to deadlock on.
type lifecycleParentIndex struct {
	mu       sync.RWMutex
	byParent map[string]map[string]struct{}

	// warmMu/warmed back ensureWarm's backfill scan — see its own doc
	// comment for why a scan is needed at all alongside Persist-time
	// maintenance, and for why this is NOT a plain sync.Once (Defect-3 fix:
	// a FAILED warm must be retried on the next call, never latched
	// forever). warmed is set true ONLY after a scan that returns a nil
	// error; warmMu serializes concurrent ensureWarm callers (both the
	// fast-path read and the scan-and-set below) so two callers can never
	// race a partial warm.
	warmMu sync.Mutex
	warmed atomic.Bool
}

// newLifecycleParentIndex returns an empty, ready-to-use index.
func newLifecycleParentIndex() *lifecycleParentIndex {
	return &lifecycleParentIndex{byParent: make(map[string]map[string]struct{})}
}

// add registers childID as a direct child of parentKey. A no-op when either
// argument is empty — an unattributable or not-yet-parented record (FR-015's
// degraded mint, or a top-level record with no ParentDurableKey at all) is
// never indexed under an empty key, which would otherwise let every such
// record collide under LifecycleFilter{ParentDurableKey: ""} — a filter
// value that means "unset" everywhere else on this struct (see
// LifecycleFilter's own doc comment). Idempotent: adding the same pair twice
// (e.g. a session's later generations, which all carry the same
// ParentDurableKey) is harmless.
func (idx *lifecycleParentIndex) add(parentKey, childID string) {
	if parentKey == "" || childID == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	children, ok := idx.byParent[parentKey]
	if !ok {
		children = make(map[string]struct{})
		idx.byParent[parentKey] = children
	}
	children[childID] = struct{}{}
}

// remove drops childID from parentKey's child set — used when childID's own
// persisted file is deleted (PruneTerminal) so a later query never has to
// discover the staleness via a failed Load. Removes the parentKey bucket
// entirely once its last child is gone, so a long-running process does not
// accumulate empty buckets forever. A no-op when the pair is not present.
func (idx *lifecycleParentIndex) remove(parentKey, childID string) {
	if parentKey == "" || childID == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	children, ok := idx.byParent[parentKey]
	if !ok {
		return
	}
	delete(children, childID)
	if len(children) == 0 {
		delete(idx.byParent, parentKey)
	}
}

// children returns a sorted, independent snapshot of parentKey's direct
// children — safe to range over after the lock is released, since the
// caller never observes the live map. A deterministic (sorted) order keeps
// List's output stable across repeated queries, matching the sorted order
// the full-scan path already produces via scanSessionIDs' sort.Strings.
func (idx *lifecycleParentIndex) children(parentKey string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	set := idx.byParent[parentKey]
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ensureWarm backfills the index from disk once per LifecycleStore instance
// — but, unlike a plain sync.Once, only a SUCCESSFUL scan is memoized
// (Defect-3 fix). A failed scan (e.g. a transient EMFILE during a fan-out
// burst) must not permanently latch that error for the process's entire
// remaining lifetime: every subsequent Stop cascade, background-shell
// cascade, approval cascade and drill-down would otherwise silently degrade
// to root-only until a restart, with no recovery path. warmMu serializes
// callers (both the fast-path check and the scan-and-set below) so
// concurrent first-callers block on the same attempt rather than each
// racing their own, exactly like sync.Once did — the difference is purely
// that a failed attempt leaves warmed false so the NEXT call retries the
// full scan instead of returning the same stale error forever.
//
// Why a scan is needed at all: FR-020 requires the index be "maintained
// inside Persist", and persistLocked does exactly that (lifecycle.go) — but
// that alone only indexes records THIS process itself has written. A record
// persisted by a PRIOR process (the common case immediately after a
// restart, before the boot sweep or any new delegation has run) exists on
// disk with a real ParentDurableKey that no in-process Persist call has ever
// seen, so relying on Persist-time maintenance alone would silently return
// zero children for every parent until something happened to re-persist
// each child — exactly the "success-shaped" silent failure this whole spec
// exists to close out. The scan closes that gap once it succeeds; every
// Persist call before, during or after it keeps the index correct for that
// session_id independently via add(), so a SUCCESSFUL warm never needs to
// repeat. A MISSING directory is legitimately not an error here:
// scanSessionIDs (lifecycle.go) returns (nil, nil) for os.IsNotExist, so a
// fresh install with no lifecycle directory yet warms successfully with an
// empty index — it is never treated as a failure to retry.
func (idx *lifecycleParentIndex) ensureWarm(s *LifecycleStore) error {
	if idx.warmed.Load() {
		return nil
	}
	idx.warmMu.Lock()
	defer idx.warmMu.Unlock()
	if idx.warmed.Load() {
		// Another goroutine completed a successful warm while we waited for
		// warmMu.
		return nil
	}

	ids, err := s.scanSessionIDs()
	if err != nil {
		// Deliberately NOT setting idx.warmed — a failed scan must be
		// retried by the next ensureWarm call, not latched forever.
		return fmt.Errorf("session: lifecycle: parent index warm-up: %w", err)
	}
	for _, id := range ids {
		rec, loadErr := s.Load(id)
		if loadErr != nil {
			if loadErr == ErrLifecycleNotFound {
				continue
			}
			// A single corrupt/unreadable record must not fail warm-up
			// for every OTHER session — log and continue, mirroring
			// PruneTerminal's own per-id error handling in lifecycle.go.
			slog.Warn("session: lifecycle: parent index warm-up: load failed, skipping",
				"session_id", id, "error", loadErr)
			continue
		}
		idx.add(rec.ParentDurableKey, rec.SessionID)
	}
	idx.warmed.Store(true)
	return nil
}
