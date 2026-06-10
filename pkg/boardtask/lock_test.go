// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Unit tests for boardtask.StripedLock.
//
// BDD scenarios:
//   Scenario: same key always maps to the same mutex
//   Scenario: different keys map to potentially different mutexes
//   Scenario: concurrent access via same key serializes (no race)
//
// Traces to: project-task-management-level1-spec.md — #407 race fix

package boardtask

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestStripedLock_SameKey_SameMutex asserts that calling Get with the same key
// multiple times returns a pointer to the same mutex object.
//
// Guards against: inconsistent shard selection breaking the serialization contract.
// Traces to: pkg/boardtask/lock.go StripedLock.Get — #407
func TestStripedLock_SameKey_SameMutex(t *testing.T) {
	sl := &StripedLock{}

	keys := []string{"task-001", "task-abc", "a", "", "01JXLONGTASKIDXXXX1234567890"}
	for _, key := range keys {
		t.Run("key="+key, func(t *testing.T) {
			mu1 := sl.Get(key)
			mu2 := sl.Get(key)
			mu3 := sl.Get(key)
			if mu1 != mu2 {
				t.Errorf("Get(%q) returned different pointers: %p vs %p", key, mu1, mu2)
			}
			if mu2 != mu3 {
				t.Errorf("Get(%q) returned different pointers: %p vs %p", key, mu2, mu3)
			}
		})
	}
}

// TestStripedLock_DifferentKeys_LikelyDifferentMutex asserts that two well-separated
// keys (that should not collide in 64 shards) map to different mutexes.
//
// Note: This is probabilistic by nature (64 shards means collisions can happen).
// We use keys that are known to hash to different shards via FNV-32a.
//
// Guards against: StripedLock ignoring the key entirely and always returning shard 0.
// Traces to: pkg/boardtask/lock.go StripedLock.Get differentiation test — #407
func TestStripedLock_DifferentKeys_LikelyDifferentMutex(t *testing.T) {
	sl := &StripedLock{}

	// Collect mutexes for 64+ distinct keys — with 64 shards, all 64 shards should
	// be exercised across these keys if distribution is non-trivial.
	seen := map[*sync.Mutex]bool{}
	for i := 0; i < 128; i++ {
		key := "task-" + string(rune('a'+i%26)) + string(rune('0'+i%10))
		mu := sl.Get(key)
		seen[mu] = true
	}
	// We should see at least 2 distinct mutexes (if all keys mapped to the same shard,
	// that would be a broken implementation).
	if len(seen) < 2 {
		t.Errorf(
			"StripedLock.Get returned only 1 distinct mutex for 128 different keys — shard selection appears broken",
		)
	}
}

// TestStripedLock_ConcurrentAccess_NoRace verifies that concurrent goroutines
// using the same key via StripedLock.Get serialize correctly — the counter
// incremented under the lock must reflect every goroutine's work.
//
// Run with -race to detect data races.
// Traces to: pkg/boardtask/lock.go StripedLock — #407 race fix
func TestStripedLock_ConcurrentAccess_NoRace(t *testing.T) {
	sl := &StripedLock{}
	const N = 100
	const key = "shared-task-id"

	var counter atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu := sl.Get(key)
			mu.Lock()
			// Read-modify-write: this is the exact pattern used in production.
			v := counter.Load()
			v++
			counter.Store(v)
			mu.Unlock()
		}()
	}

	wg.Wait()
	if counter.Load() != N {
		t.Errorf("counter = %d, want %d; concurrent StripedLock serialization failed", counter.Load(), N)
	}
}

// TestTaskFileLock_PackageLevelSingleton asserts the package-level singleton is
// non-nil and returns consistent mutexes — the production gateway uses this instance.
//
// Traces to: pkg/boardtask/lock.go TaskFileLock — #407
func TestTaskFileLock_PackageLevelSingleton(t *testing.T) {
	if TaskFileLock == nil {
		t.Fatal("TaskFileLock must be non-nil (package-level singleton)")
	}

	// Same key → same mutex on the singleton.
	mu1 := TaskFileLock.Get("task-singleton-key")
	mu2 := TaskFileLock.Get("task-singleton-key")
	if mu1 != mu2 {
		t.Errorf("TaskFileLock.Get returned different pointers for same key: %p vs %p", mu1, mu2)
	}
}
