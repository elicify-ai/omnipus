// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import "context"

// MV-11 (docs/internal/specs/unified-search-and-grep-spec.md): ONE shared
// 2-slot walk semaphore covers every filegrep.Search call in the process,
// REST (POST /library/{workspace_id}/files/search) and the agent `grep`
// tool alike — an agent flood and a human typing in the Library search bar
// must observe the SAME two slots, not two independently counted ones.
//
// This lives in pkg/filegrep, not pkg/gateway, for exactly one reason: the
// agent tool is built in pkg/tools, and pkg/tools cannot import pkg/gateway
// (pkg/gateway imports pkg/agent which imports pkg/tools — the reverse edge
// would be a cycle). pkg/filegrep is already the one package both the REST
// handler and the tool import for Search itself, so it is the only place a
// single, truly shared counter can live without inventing a new leaf
// package just to hold two functions.
//
// Package-level and process-global by design (Hard Constraint #1: a single
// Go binary) — both callers run in the same process, so a package-level
// channel is the whole implementation; there is no cross-process concern to
// design around.
var walkSlots = make(chan struct{}, 2)

// TryAcquire blocks until a walk slot is free or ctx is done, whichever
// comes first. It returns true with a slot held — the caller MUST call
// Release exactly once, and only after a true return — or false if ctx
// expired (or was already done) before a slot became free.
//
// A caller wanting the tool-side "wait up to 2s, then a structured busy
// error" contract (MV-11) passes a context.WithTimeout(parent, 2*time.Second)
// derivative; REST's own 429-with-Retry-After path is expected to pass one
// with no wait at all (an immediately-expired/canceled context), so a third
// concurrent walk is refused rather than queued behind two long ones.
func TryAcquire(ctx context.Context) bool {
	select {
	case walkSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// Release frees a slot acquired by a successful TryAcquire call. Calling it
// without a matching successful TryAcquire is a caller bug — it would let a
// third walk start while two others believe they still hold both slots —
// so every TryAcquire==true must be paired with exactly one Release, and a
// TryAcquire==false must never be paired with one.
func Release() {
	<-walkSlots
}
