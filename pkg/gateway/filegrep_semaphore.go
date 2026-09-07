// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// filegrep walk concurrency cap (MV-11 / R2-MAJ-003,
// docs/internal/specs/unified-search-and-grep-spec.md)
//
// ONE shared 2-slot semaphore covers EVERY filegrep walk regardless of which
// surface started it. Two consumers draw from it:
//
//   - the human Library file search — POST
//     /api/v1/library/{workspace_id}/files/search
//     (pkg/gateway/rest_library_files_search.go, handleLibraryFilesSearch)
//   - the agent `grep` tool (a pkg/tools.Tool adapter over the same
//     pkg/filegrep engine — see the spec's implementation note for the
//     vaultprops.FindTool-shaped pattern it follows)
//
// A filegrep walk is a bounded but potentially expensive filesystem scan; the
// spec caps how many run at once ACROSS BOTH SURFACES TOGETHER, not per
// surface — a person hammering the Library search bar and an agent looping
// on grep must draw from the same two slots, or the cap does not actually
// bound anything. AcquireFilegrepWalkSlot is exported (package gateway) so a
// grep-tool adapter reaches this exact singleton whether it lives in this
// package or, more likely, imports it from one that does — there is only
// ever one semaphore process-wide.
// ---------------------------------------------------------------------------

// filegrepWalkSlotCount is MV-11's "2-slot" cap, named once so the semaphore
// and anything describing its capacity (docs, tests) read from one place.
const filegrepWalkSlotCount = 2

// filegrepWalkSlots is the semaphore itself: a buffered channel used as a
// counting semaphore (send to acquire, receive to release).
var filegrepWalkSlots = make(chan struct{}, filegrepWalkSlotCount)

// AcquireFilegrepWalkSlot reserves one of the two shared filegrep walk slots.
//
// It tries a non-blocking acquire first; if both slots are busy it waits up
// to wait for one to free — the caller's "brief try" before giving up (MV-11:
// the REST handler uses a short wait before answering 429 + Retry-After; a
// tool-side caller can pass a longer wait, e.g. up to the spec's 2s, for its
// own structured busy error) — or until ctx is done, whichever comes first.
//
// ok is false when neither happened before the deadline: the caller is busy
// and MUST refuse the request rather than run the walk unbounded.
//
// On ok=true the caller MUST invoke release exactly once when its walk
// finishes (typically `defer release()` immediately after a successful
// acquire) — skipping it leaks a slot for the remaining life of the process.
func AcquireFilegrepWalkSlot(ctx context.Context, wait time.Duration) (release func(), ok bool) {
	select {
	case filegrepWalkSlots <- struct{}{}:
		return filegrepWalkSlotRelease, true
	default:
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case filegrepWalkSlots <- struct{}{}:
		return filegrepWalkSlotRelease, true
	case <-timer.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

// filegrepWalkSlotRelease frees one slot. A single package-level func value
// (rather than a closure allocated per acquire) since releasing needs no
// per-call state beyond the shared channel itself.
func filegrepWalkSlotRelease() {
	<-filegrepWalkSlots
}
