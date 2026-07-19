// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

import (
	"hash/fnv"
	"sync"
)

const numLockShards = 64

// StripedLock is a fixed-size sharded mutex pool. It maps an arbitrary string
// key to one of 64 mutexes via FNV-32a hash, providing per-entity
// serialization with O(1) memory regardless of entity count. Mirrors
// task.StripedLock exactly (pkg/task/lock.go) but keyed in its own
// namespace — plan IDs and task IDs never contend for the same shard pool.
//
// Usage:
//
//	mu := lock.Get(planID)
//	mu.Lock()
//	defer mu.Unlock()
type StripedLock struct {
	locks [numLockShards]sync.Mutex
}

// Get returns the mutex shard for the given key. The same key always maps to
// the same mutex within a StripedLock instance.
func (s *StripedLock) Get(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &s.locks[h.Sum32()%numLockShards]
}

// PlanFileLock is the process-wide shared StripedLock for plan files. Every
// read-modify-write on a plan file (Create/Update/Delete) acquires this lock
// keyed by plan ID before touching the file, mirroring task.TaskFileLock's
// role for task files.
//
//nolint:gochecknoglobals
var PlanFileLock = &StripedLock{}
