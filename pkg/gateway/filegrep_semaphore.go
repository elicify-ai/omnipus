// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"time"

	"github.com/elicify-ai/omnipus/pkg/filegrep"
)

// ---------------------------------------------------------------------------
// filegrep walk concurrency cap (MV-11 / R2-MAJ-003,
// docs/internal/specs/unified-search-and-grep-spec.md)
//
// ONE shared 2-slot semaphore covers EVERY filegrep walk regardless of which
// surface started it — and that one semaphore is pkg/filegrep's own
// (filegrep.TryAcquire / filegrep.Release), NOT a channel owned by this
// package. Two consumers draw from those same two slots:
//
//   - the human Library file search — POST
//     /api/v1/library/{workspace_id}/files/search
//     (pkg/gateway/rest_library_files_search.go, handleLibraryFilesSearch),
//     through AcquireFilegrepWalkSlot below
//   - the agent `grep` tool (pkg/tools/grep.go), which calls
//     filegrep.TryAcquire directly with its own 2s busy-wait
//
// The counter lives in pkg/filegrep because pkg/tools cannot import
// pkg/gateway (pkg/gateway → pkg/agent → pkg/tools; the reverse edge would
// be a cycle), and pkg/filegrep is the one package both surfaces already
// import for Search itself. An earlier revision of this file owned its own
// private 2-slot channel — which would have counted the REST and tool
// surfaces INDEPENDENTLY, two caps of 2 instead of one shared cap of 2,
// exactly what MV-11 forbids ("a person hammering the Library search bar
// and an agent looping on grep must draw from the same two slots, or the
// cap does not actually bound anything"). This wrapper now exists only to
// express the REST handler's acquisition POLICY (instant try, then a brief
// bounded wait, then 429) over the shared slots.
// ---------------------------------------------------------------------------

// AcquireFilegrepWalkSlot reserves one of the two shared filegrep walk slots
// — the same two the agent grep tool draws from.
//
// It tries a non-blocking acquire first; if both slots are busy it waits up
// to wait for one to free — the caller's "brief try" before giving up (MV-11:
// the REST handler uses a short wait before answering 429 + Retry-After) —
// or until ctx is done, whichever comes first. A wait of zero (or negative)
// means "refuse unless a slot is instantly free", deterministically.
//
// ok is false when no slot was obtained in time: the caller is busy and MUST
// refuse the request rather than run the walk unbounded.
//
// On ok=true the caller MUST invoke release exactly once when its walk
// finishes (typically `defer release()` immediately after a successful
// acquire) — skipping it leaks a slot for the remaining life of the process.
func AcquireFilegrepWalkSlot(ctx context.Context, wait time.Duration) (release func(), ok bool) {
	if filegrep.TryAcquireNow() {
		return filegrep.Release, true
	}
	if wait <= 0 {
		return nil, false
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if filegrep.TryAcquire(waitCtx) {
		return filegrep.Release, true
	}
	return nil, false
}
