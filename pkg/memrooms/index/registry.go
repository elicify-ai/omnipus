// Process-global, reference-counted registry of open bleve scorch RoomIndex
// handles, keyed by the absolute, cleaned on-disk index path.
//
// WHY THIS EXISTS — shared-workspace-room deadlock:
//
// bleve's scorch engine stores its root metadata in a bbolt (boltdb) file that
// is opened with a *process-exclusive, infinite-wait* file lock. Only ONE open
// handle per file is permitted process-wide; a second bbolt.Open on the same
// file blocks FOREVER waiting for the first handle to close.
//
// Each agent.MemoryStore historically kept its own per-instance index cache and
// called bleve.Open independently. The WORKSPACE memory room
// (<workspace>/.omnipus/) is SHARED across agents, and v0.1.0 runs multiple
// agents concurrently (orchestrator, delegation, max-parallel sessions). Two
// MemoryStores accessing the same workspace room would each try to open the
// shared scorch index — the second bbolt.Open would deadlock the whole process.
//
// FIX: open the bbolt-backed scorch index AT MOST ONCE per absolute path and
// share the single *RoomIndex across every MemoryStore that asks for it. The
// registry refcounts holders so the underlying bbolt handle is opened on first
// acquire and physically closed only when the LAST holder releases it. bleve's
// scorch engine is internally goroutine-safe for concurrent reads + a single
// serialized writer (RoomIndex.mu serializes writes), so sharing one handle
// across MemoryStores and goroutines is safe.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package index

import (
	"path/filepath"
	"sync"
)

// registry is the process-global table of open, shared RoomIndex handles.
//
// It is keyed by the absolute, filepath.Clean'd index directory path so that
// two callers naming the same on-disk index by different (relative vs absolute,
// or differently-cleaned) paths still share ONE bbolt handle. The mutex guards
// both the map and the per-entry refcount; it is held only for the bookkeeping
// around acquire/release, never across a Search/Index call.
var registry = struct {
	mu      sync.Mutex
	entries map[string]*registryEntry
}{
	entries: make(map[string]*registryEntry),
}

// registryEntry is one shared RoomIndex plus its live holder count.
type registryEntry struct {
	ri   *RoomIndex
	refs int
}

// registryKey returns the canonical registry key for a room's index directory:
// the absolute, cleaned path. Falls back to the cleaned relative path if the
// working directory cannot be resolved (filepath.Abs only fails when os.Getwd
// fails, which is effectively never for a running process).
func registryKey(idxPath string) string {
	abs, err := filepath.Abs(idxPath)
	if err != nil {
		return filepath.Clean(idxPath)
	}
	return filepath.Clean(abs)
}

// acquireShared returns the shared RoomIndex for key, opening it via open()
// exactly once. On the first acquire for a key it calls open() (a single
// bleve.Open / bbolt open) and records refs=1; subsequent acquires for the same
// key reuse the already-open handle and increment refs. open() is invoked while
// the registry mutex is held so that two concurrent first-acquirers cannot both
// race into bleve.Open on the same path (which is the deadlock we are avoiding).
func acquireShared(key string, open func() (*RoomIndex, error)) (*RoomIndex, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if e, ok := registry.entries[key]; ok {
		e.refs++
		return e.ri, nil
	}

	ri, err := open()
	if err != nil {
		return nil, err
	}
	ri.regKey = key
	registry.entries[key] = &registryEntry{ri: ri, refs: 1}
	return ri, nil
}

// releaseShared decrements the holder count for the RoomIndex registered under
// key and, when the count reaches zero, removes the entry and physically closes
// the underlying bleve/bbolt handle (returning any close error). While a holder
// remains, it returns nil without touching the live handle so that one
// MemoryStore's Close() can never close an index another MemoryStore is still
// using. A key with no registry entry (double-close, or a handle that was never
// registry-managed) is a safe no-op.
func releaseShared(key string) error {
	registry.mu.Lock()
	e, ok := registry.entries[key]
	if !ok {
		registry.mu.Unlock()
		return nil
	}
	e.refs--
	if e.refs > 0 {
		registry.mu.Unlock()
		return nil
	}
	delete(registry.entries, key)
	registry.mu.Unlock()

	// Last holder released — close the real handle outside the registry lock.
	return e.ri.closeUnderlying()
}
