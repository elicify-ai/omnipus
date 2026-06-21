// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// memory_sharedroom_race_test.go — race regression for MemoryStore.sharedRoom.
//
// Before the fix, SetWorkspaceID wrote ms.sharedRoom without synchronization
// while rooms()/resolveWriteRoom()/SearchEntriesInScope()/SharedRoom() read it
// concurrently, allowing one workspace's shared room to leak into another's
// reads. These tests drive that exact interleaving under `go test -race`.

package agent

import (
	"fmt"
	"sync"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/memrooms"
)

// TestSharedRoom_ConcurrentSetAndRead races SetWorkspaceID against every reader
// of sharedRoom. Must be -race clean.
func TestSharedRoom_ConcurrentSetAndRead(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)
	defer ms.Close()

	const goroutines = 8
	const iters = 200

	var wg sync.WaitGroup

	// Writers: continuously swap the workspace ID (including clearing it).
	for w := 0; w < goroutines; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if i%5 == 0 {
					ms.SetWorkspaceID("")
					continue
				}
				ms.SetWorkspaceID(fmt.Sprintf("workspace-%d-%d", w, i))
			}
		}()
	}

	// Readers: exercise every accessor that touches sharedRoom.
	for r := 0; r < goroutines; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = ms.SharedRoom()
				_ = ms.rooms()
				_ = ms.resolveWriteRoom(memrooms.RoomScopeShared)
			}
		}()
	}

	wg.Wait()
}

// TestSharedRoom_ConcurrentSearchAndSet races SearchEntriesInScope (which reads
// sharedRoom) against SetWorkspaceID. Must be -race clean.
func TestWorkspaceMemory_ConcurrentSearchAndSet(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)
	defer ms.Close()

	// Seed a memory so the search path does real work.
	if err := ms.AppendLongTerm("seed memory for race test", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	const goroutines = 6
	const iters = 100
	var wg sync.WaitGroup

	for w := 0; w < goroutines; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ms.SetWorkspaceID(fmt.Sprintf("ws-%d-%d", w, i))
			}
		}()
	}

	for r := 0; r < goroutines; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if _, err := ms.SearchEntriesInScope("memory", 10, memrooms.RoomScopeBoth); err != nil {
					t.Errorf("SearchEntriesInScope: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()
}
