// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 W15/W15b, FR-048, FR-050, FR-101 (U4) — UnifiedStore's per-session
// striped lock and the FR-101 lock-order observation seam.
//
// Before this file, UnifiedStore.mu was a single store-global sync.RWMutex
// (unified.go:161, pre-ADR-057): NewSession held it across two fsync+rename
// cycles plus a directory fsync, and AppendTranscript held it on every
// streamed transcript line, across the append AND a full meta rewrite. Under
// ADR-057 every delegation becomes a real session, so a wide fan-out
// serializes N fsync-bound creates behind one mutex and stalls token
// streaming in every OTHER session in the store (R-8).
//
// FR-048 replaces UnifiedStore.mu with two independent primitives:
//
//  1. A 64-shard FNV-32a keyed mutex pool (u4SessionStripedLock, this file),
//     one shard per session id, giving concurrent sessions independent lock
//     paths. Shape copied VERBATIM from pkg/session/lifecycle_lock.go:17-39's
//     lifecycleStripedLock — do not reinvent the hashing scheme.
//  2. A narrow us.cacheMu (declared in unified.go, next to metaCache) guarding
//     ONLY metaCache, cacheLoadFailures, and the FR-097 parent index — never
//     held across an os.* or fileutil.* call (FR-049).
//
// Lock order (FR-050): sessionLock(id) -> cacheMu, one-directional. Two
// session shards are NEVER held at once, with exactly two exceptions:
//   - ClearAll/RetentionSweep, which take EVERY shard in ASCENDING INDEX
//     order via lockAllSessionShards (FR-050(a)) — a full-store stall,
//     explicitly accepted (spec Ambiguity item 13): the operation is already
//     effectively store-global today (ClearAll already holds one write lock
//     for its entire call; RetentionSweep gains the equivalent scope here),
//     so this is not a throughput regression.
//   - The parent-Owner copy inside U2's CreateSessionWithID (FR-082): read
//     the parent's meta under lockSession(parentID), Unlock it, THEN
//     lockSession(childID) to create — never both held simultaneously. This
//     file supplies the primitive (lockSession); the two-shard PROTOCOL is
//     enforced by the caller's discipline, not by the type system, exactly
//     like calling (*sync.Mutex).Lock/Unlock correctly is a caller
//     discipline everywhere else in Go.
//
// FR-101 lock-order seam: lock-acquisition ORDER (BDD-92/SC-038) and the
// ClearAll/RetentionSweep full-sweep order have no on-disk artefact to
// assert on, and go test -race is not a lock-order checker — it reports
// nothing for an inversion that does not happen to deadlock in the run under
// test. So every shard acquire/release in this package funnels through the
// package-level sessionLockAcquireFn/sessionLockReleaseFn vars below rather
// than calling (*sync.Mutex).Lock/Unlock directly. The default implementation
// IS the direct call; a test in this package (and ONLY a test in this
// package — these are unexported) may swap them to also record (shard,
// acquire-or-release) before delegating to the same default, making the
// recorded order observable without weakening real mutual exclusion.
//
// Cross-unit contract (spec line ~1007): U2's CreateSessionWithID
// (pkg/session/unified_api.go) depends on lockSession's exact name and
// signature to implement FR-082's protocol. Do not rename it.
package session

import (
	"hash/fnv"
	"sync"
)

// u4NumSessionLockShards is the session-store shard count (FR-048(a)),
// matching pkg/session/lifecycle_lock.go's 64-shard convention.
const u4NumSessionLockShards = 64

// u4SessionStripedLock is the 64-shard FNV-32a keyed mutex pool backing
// UnifiedStore's per-session locking. Shape copied verbatim from
// pkg/session/lifecycle_lock.go:17-39's lifecycleStripedLock — a fixed-size
// array of shards, O(1) memory regardless of session count, no reinvented
// hashing scheme.
type u4SessionStripedLock struct {
	locks [u4NumSessionLockShards]sync.Mutex
}

// u4ShardFor returns the shard index for key using the same FNV-32a scheme
// as lifecycleStripedLock.Get. It is a free function (not just a method) so
// a test can compute shard(parentID)/shard(childID) independently of any
// UnifiedStore instance (BDD-92's "acquire(shard(parent))" assertions).
func u4ShardFor(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key)) // hash.Hash.Write never returns an error
	return h.Sum32() % u4NumSessionLockShards
}

// shard resolves key to its index and backing mutex in one hash computation.
func (s *u4SessionStripedLock) shard(key string) (uint32, *sync.Mutex) {
	idx := u4ShardFor(key)
	return idx, &s.locks[idx]
}

// sessionLockAcquireFn / sessionLockReleaseFn are the FR-101 lock-order
// observation seam described in this file's doc comment. NEVER exported —
// overriding from outside this package would defeat the seam's purpose of
// letting only an in-package test observe real acquisition order.
var (
	sessionLockAcquireFn = func(shard uint32, mu *sync.Mutex) { mu.Lock() }
	sessionLockReleaseFn = func(shard uint32, mu *sync.Mutex) { mu.Unlock() }
)

// sessionLockHandle is one acquired session shard. Unlock MUST be called
// exactly once, and releases through the same FR-101 seam that acquired it.
type sessionLockHandle struct {
	shard uint32
	mu    *sync.Mutex
}

// Unlock releases the shard this handle holds.
func (h *sessionLockHandle) Unlock() {
	sessionLockReleaseFn(h.shard, h.mu)
}

// lockSession acquires the shard for the given session id (FR-048(a)) through
// the FR-101 seam and returns a handle whose Unlock releases it.
//
// This is the SOLE entry point for per-session locking in the store — every
// method in unified.go that used to take us.mu now takes exactly one
// session's shard via this method for the duration of its filesystem work
// (and, where applicable, the narrower cacheMu nested inside it).
//
// U2's CreateSessionWithID (pkg/session/unified_api.go, cross-unit request,
// spec line ~1007) depends on this exact method name and signature to
// implement FR-082's two-shard protocol: read the parent's meta under
// lockSession(parentID), Unlock it, THEN lockSession(childID) to create the
// child — never both held at once (FR-050).
func (us *UnifiedStore) lockSession(id string) *sessionLockHandle {
	shard, mu := us.sessionLocks.shard(id)
	sessionLockAcquireFn(shard, mu)
	return &sessionLockHandle{shard: shard, mu: mu}
}

// lockAllSessionShards acquires every one of the store's 64 shards in
// STRICTLY ASCENDING index order through the same FR-101 seam, and returns a
// func that releases them (also in ascending index order — release order is
// not constrained by FR-050, but symmetry keeps this simple to reason about
// and to verify against SC-038's "records ... acquiring all 64 shards in
// strictly ascending index order").
//
// This is the FR-050(a) exception: ClearAll and RetentionSweep are the only
// two operations allowed to hold more than one session shard at a time, and
// they MUST do so in a FIXED (index, not hash) order so they can never invert
// against lockSession's parent-then-child ordering or against each other.
//
// Callers that hold the result of lockAllSessionShards MUST NOT also call
// lockSession for any id — sync.Mutex is not reentrant, and every shard is
// already held by this same goroutine. Touch metaCache/cacheLoadFailures/the
// parent index directly via cacheMu instead (see ClearAll/RetentionSweep).
func (us *UnifiedStore) lockAllSessionShards() (unlock func()) {
	for i := uint32(0); i < u4NumSessionLockShards; i++ {
		sessionLockAcquireFn(i, &us.sessionLocks.locks[i])
	}
	return func() {
		for i := uint32(0); i < u4NumSessionLockShards; i++ {
			sessionLockReleaseFn(i, &us.sessionLocks.locks[i])
		}
	}
}
