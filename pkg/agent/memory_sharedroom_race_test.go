// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/memrooms"
)

// TestSharedRoom_ConcurrentSetAndRead retains the concurrent resolver/accessor
// regression after removing mutable room binding. Every operation must resolve
// its OWN workspace, not merely avoid a Go data race on the old pointer.
func TestSharedRoom_ConcurrentSetAndRead(t *testing.T) {
	ms := NewMemoryStore(t.TempDir(), t.TempDir())
	defer ms.Close()

	const goroutines = 8
	const iters = 200
	var wg sync.WaitGroup
	for w := 0; w < goroutines; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ws := fmt.Sprintf("workspace-%d-%d", w, i)
				identity := ms.scopeForWorkspace(ws)
				want := memrooms.ResolveWorkspaceSharedRoom(ms.omnipusHome, ws)
				got := ms.SharedRoom(identity)
				if got == nil || *got != want {
					t.Errorf("SharedRoom(%s) = %+v, want %+v", ws, got, want)
				}
			}
		}()
	}
	for r := 0; r < goroutines; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ws := fmt.Sprintf("reader-%d-%d", r, i)
				identity := ms.scopeForWorkspace(ws)
				want := memrooms.ResolveWorkspaceSharedRoom(ms.omnipusHome, ws)
				rooms := ms.rooms(identity)
				if rooms.Private != ms.PrivateRoom() || rooms.Shared == nil || *rooms.Shared != want {
					t.Errorf("rooms(%s) = %+v, want own private + %+v", ws, rooms, want)
				}
				if got := ms.resolveWriteRoom(memrooms.RoomScopeShared, identity); got != want {
					t.Errorf("resolveWriteRoom(%s) = %+v, want %+v", ws, got, want)
				}
			}
		}()
	}
	wg.Wait()
}

// TestWorkspaceMemory_ConcurrentSearchAndSet keeps the concurrent search / room
// resolution workload. The identity is now passed by value instead of swapped
// on the agent store. Run under -race in CI.
func TestWorkspaceMemory_ConcurrentSearchAndSet(t *testing.T) {
	ms := NewMemoryStore(t.TempDir(), t.TempDir())
	defer ms.Close()
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
				ws := fmt.Sprintf("ws-%d-%d", w, i)
				want := memrooms.ResolveWorkspaceSharedRoom(ms.omnipusHome, ws)
				if got := ms.SharedRoom(ms.scopeForWorkspace(ws)); got == nil || *got != want {
					t.Errorf("SharedRoom(%s) = %+v, want %+v", ws, got, want)
				}
			}
		}()
	}
	for r := 0; r < goroutines; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identity := ms.scopeForWorkspace(fmt.Sprintf("reader-%d", r))
			for i := 0; i < iters; i++ {
				entries, err := ms.SearchEntriesInScope("memory", 10, memrooms.RoomScopeBoth, identity)
				if err != nil {
					t.Errorf("SearchEntriesInScope: %v", err)
					return
				}
				// serializeMemoryFile adds a canonical terminal newline; the
				// parser preserves it. Assert the exact serialized body.
				if len(entries) != 1 || entries[0].Content != "seed memory for race test\n" {
					t.Errorf("private seed lost during concurrent room resolution: %+v", entries)
				}
			}
		}()
	}
	wg.Wait()
}
