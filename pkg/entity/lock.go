// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package entity

import (
	"hash/fnv"
	"sync"
)

const numLockShards = 64

// stripedLock is a fixed-size sharded mutex pool. It maps an arbitrary
// string key to one of 64 mutexes via FNV-32a hash, providing per-entity
// in-process serialization with O(1) memory regardless of entity count —
// the same pattern pkg/task (StripedLock) and pkg/plan (StripedLock) already
// ship (ADR-054 §1: "the three packages already using per-entity files").
type stripedLock struct {
	locks [numLockShards]sync.Mutex
}

// get returns the mutex shard for the given key. The same key always maps to
// the same mutex within a stripedLock instance.
func (s *stripedLock) get(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &s.locks[h.Sum32()%numLockShards]
}

// fileLock is the single, process-wide stripedLock shared by every Store[T]
// instance regardless of T or directory. The lock key is always the full
// data-file path (Store.path(id), not the bare id), so two Store instances
// constructed over the same directory — or two different entity kinds whose
// IDs happen to collide as bare strings (an agent "abc" and a channel "abc"
// living under different entities/<kind>/ directories) — still serialize
// correctly. This mirrors pkg/task.TaskFileLock's process-wide-global
// approach but keys on the full path instead of relying on "one canonical
// Store per process" being true, since this package is meant to be
// instantiated once per entity kind (entities/agents, entities/channels,
// ...), not once system-wide like pkg/task.
var fileLock = &stripedLock{}
