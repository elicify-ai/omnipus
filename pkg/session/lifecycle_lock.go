// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"hash/fnv"
	"sync"
)

// numLifecycleLockShards mirrors pkg/task/lock.go's numLockShards (64) and
// pkg/plan/lock.go's own copy — this package follows the same
// one-striped-lock-per-package convention rather than sharing a single
// cross-package instance, so a hot lifecycle-record write never contends
// with an unrelated task/plan write on the same shard.
const numLifecycleLockShards = 64

// lifecycleStripedLock is a fixed-size sharded mutex pool mapping an
// arbitrary durable session_id to one of 64 mutexes via FNV-32a hash,
// providing per-entity serialization with O(1) memory regardless of session
// count (ADR-053 S2 — "persisted per-entity JSONL ... 64-shard mutex pool").
//
// Usage:
//
//	mu := lock.Get(sessionID)
//	mu.Lock()
//	defer mu.Unlock()
type lifecycleStripedLock struct {
	locks [numLifecycleLockShards]sync.Mutex
}

// Get returns the mutex shard for the given key. The same key always maps to
// the same mutex within a lifecycleStripedLock instance.
func (s *lifecycleStripedLock) Get(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &s.locks[h.Sum32()%numLifecycleLockShards]
}
