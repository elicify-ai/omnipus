// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"hash/fnv"
	"sync"
)

const numLockShards = 64

// StripedLock is a fixed-size sharded mutex pool. It maps an arbitrary string
// key to one of 64 mutexes via FNV-32a hash, providing per-entity
// serialization with O(1) memory regardless of entity count.
//
// Usage:
//
//	mu := lock.Get(taskID)
//	mu.Lock()
//	defer mu.Unlock()
//
// This is the canonical shared lock for unified task read-modify-write paths
// used by both the gateway REST handlers and the task tools. Both must acquire
// this lock keyed by task ID before mutating a task file to prevent
// interleaving updates.
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

// TaskFileLock is the process-wide shared StripedLock for unified task files.
// Both the REST gateway handlers and the task tools must acquire this lock
// keyed by task ID before performing a read-modify-write on any task file.
//
//nolint:gochecknoglobals
var TaskFileLock = &StripedLock{}
