// Package index — bleve BM25 recall tests (FR-7.4).
//
// Testing policy: written but NOT run locally (CI authority per CLAUDE.md).
// Run via: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestBleve' -p 1 ./pkg/memrooms/index/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package index_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/memrooms"
	memindex "github.com/dapicom-ai/omnipus/pkg/memrooms/index"
)

// makeTestRoom creates a temporary room directory and returns a memrooms.Room.
func makeTestRoom(t *testing.T) memrooms.Room {
	t.Helper()
	root := t.TempDir()
	memoriesDir := filepath.Join(root, "memories")
	if err := os.MkdirAll(memoriesDir, 0o700); err != nil {
		t.Fatalf("makeTestRoom: mkdir %s: %v", memoriesDir, err)
	}
	return memrooms.Room{
		Root:            root,
		MemoriesDir:     memoriesDir,
		CountersPath:    filepath.Join(root, "counters.jsonl"),
		LastSessionPath: filepath.Join(root, "last-session.md"),
	}
}

// writeTestMemory creates a MemoryFile and writes it to the room.
func writeTestMemory(t *testing.T, room memrooms.Room, id, title, body string) memrooms.MemoryFile {
	t.Helper()
	mf := memrooms.MemoryFile{
		Frontmatter: memrooms.MemoryFrontmatter{
			ID:         id,
			Title:      title,
			Type:       memrooms.MemoryTypeFact,
			Tags:       []string{"test"},
			Confidence: 0,
			Status:     memrooms.MemoryStatusActive,
			Supersedes: "",
			Author:     "agent-test",
			BornIn:     "session-001",
		},
		Body: body,
	}
	if err := memrooms.WriteMemoryFile(room.MemoriesDir, mf); err != nil {
		t.Fatalf("writeTestMemory: %v", err)
	}
	return mf
}

// TestBleveBM25_NoEmbeddings verifies that recall returns BM25 matches and that
// no vector/embedding/SQLite dependencies are involved.
// Traces to: US-4 / AC-1 / TDD #4.
func TestBleveBM25_NoEmbeddings(t *testing.T) {
	room := makeTestRoom(t)

	// Write two memories; one contains "prometheus" and one does not.
	m1 := writeTestMemory(t, room, "mem-001", "Prometheus monitoring", "We use prometheus for metrics and alerting in production.")
	_ = writeTestMemory(t, room, "mem-002", "Deployment notes", "The service deploys to Kubernetes via Helm charts.")

	ri, err := memindex.OpenOrCreate(room)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	defer ri.Close()

	// Index both memories.
	all, err := memrooms.ScanMemories(room.MemoriesDir)
	if err != nil {
		t.Fatalf("ScanMemories: %v", err)
	}
	for _, mf := range all {
		if idxErr := ri.Index(mf); idxErr != nil {
			t.Fatalf("Index(%s): %v", mf.Frontmatter.ID, idxErr)
		}
	}

	hits, err := ri.Search("prometheus", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(hits) == 0 {
		t.Fatal("expected at least one BM25 hit for 'prometheus', got none")
	}
	if hits[0].ID != m1.Frontmatter.ID {
		t.Errorf("expected top hit to be %q, got %q", m1.Frontmatter.ID, hits[0].ID)
	}
	if hits[0].Score <= 0 {
		t.Errorf("expected positive BM25 score, got %v", hits[0].Score)
	}

	// Verify no embedding / SQLite / vector deps are loaded (compile-time constraint —
	// build with CGO_ENABLED=0 already asserts this at the build gate).
}

// TestBleveIndex_RebuildFromMd verifies that deleting the bleve index and calling
// OpenOrCreate rebuilds it from the .md sources (FR-7.4 derived index).
// Traces to: US-4 / AC-2 / TDD #5.
func TestBleveIndex_RebuildFromMd(t *testing.T) {
	room := makeTestRoom(t)

	m1 := writeTestMemory(t, room, "mem-rebuild-001", "Secret recipe", "The secret is in the sauce and basil leaves.")
	_ = time.Now() // silence unused import

	// Open, index, close.
	ri, err := memindex.OpenOrCreate(room)
	if err != nil {
		t.Fatalf("OpenOrCreate (first): %v", err)
	}
	if idxErr := ri.Index(m1); idxErr != nil {
		t.Fatalf("Index: %v", idxErr)
	}
	if closeErr := ri.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	// Wipe the bleve index directory to simulate corruption/deletion.
	idxDir := filepath.Join(room.Root, memindex.IndexSubdir)
	if err := os.RemoveAll(idxDir); err != nil {
		t.Fatalf("RemoveAll bleve index: %v", err)
	}

	// Re-open: OpenOrCreate must rebuild from .md files.
	ri2, err := memindex.OpenOrCreate(room)
	if err != nil {
		t.Fatalf("OpenOrCreate (rebuild): %v", err)
	}
	defer ri2.Close()

	hits, err := ri2.Search("basil", 10)
	if err != nil {
		t.Fatalf("Search after rebuild: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected a hit for 'basil' after rebuild from .md, got none")
	}
	if hits[0].ID != m1.Frontmatter.ID {
		t.Errorf("expected hit ID %q after rebuild, got %q", m1.Frontmatter.ID, hits[0].ID)
	}
}

// TestBleveIndex_BM25RankingOrder verifies that the more-relevant document
// receives a higher BM25 score than a less-relevant one.
// Traces to: US-4 / TDD #4.
func TestBleveIndex_BM25RankingOrder(t *testing.T) {
	room := makeTestRoom(t)

	// mem-rank-001 mentions the query term 4 times (higher TF).
	// mem-rank-002 mentions the query term once.
	m1 := writeTestMemory(t, room, "mem-rank-001", "Kafka events",
		"Kafka is a distributed streaming platform. Kafka handles high throughput. "+
			"We use Kafka for event sourcing. Our Kafka cluster has 12 brokers.")
	m2 := writeTestMemory(t, room, "mem-rank-002", "Database notes",
		"PostgreSQL is the primary database. We briefly evaluated Kafka but chose Postgres.")

	ri, err := memindex.OpenOrCreate(room)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	defer ri.Close()

	for _, mf := range []memrooms.MemoryFile{m1, m2} {
		if idxErr := ri.Index(mf); idxErr != nil {
			t.Fatalf("Index(%s): %v", mf.Frontmatter.ID, idxErr)
		}
	}

	hits, err := ri.Search("kafka", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	// Higher TF document must score higher.
	if hits[0].ID != m1.Frontmatter.ID {
		t.Errorf("expected mem-rank-001 (higher TF) as top hit, got %q", hits[0].ID)
	}
	if hits[0].Score <= hits[1].Score {
		t.Errorf("expected score[0] (%.4f) > score[1] (%.4f)", hits[0].Score, hits[1].Score)
	}
}

// TestBleveIndex_EmptyQueryReturnsAll verifies that an empty query returns
// all indexed documents (match-all behaviour).
func TestBleveIndex_EmptyQueryReturnsAll(t *testing.T) {
	room := makeTestRoom(t)

	writeTestMemory(t, room, "mem-all-001", "Memory A", "Body A for testing.")
	writeTestMemory(t, room, "mem-all-002", "Memory B", "Body B for testing.")
	writeTestMemory(t, room, "mem-all-003", "Memory C", "Body C for testing.")

	ri, err := memindex.OpenOrCreate(room)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	defer ri.Close()

	// Rebuild to pick up all 3 memories.
	if err := ri.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	hits, err := ri.Search("", 50)
	if err != nil {
		t.Fatalf("Search empty query: %v", err)
	}
	if len(hits) != 3 {
		t.Errorf("expected 3 hits for empty query, got %d", len(hits))
	}
}

// TestBleveIndex_SharedHandleNoDeadlock is the regression test for the
// shared-workspace-room deadlock: two OpenOrCreate calls on the SAME on-disk
// index path must each succeed without a second bbolt.Open (which holds a
// process-exclusive, infinite-wait file lock). Before the process-global
// refcounted registry, the second OpenOrCreate blocked FOREVER in
// scorch.openBolt. We assert both opens return the SAME shared *RoomIndex and
// that both can build/search concurrently, all within a hard timeout so the test
// FAILS FAST (rather than hanging the whole suite) if the deadlock regresses.
func TestBleveIndex_SharedHandleNoDeadlock(t *testing.T) {
	room := makeTestRoom(t)
	writeTestMemory(t, room, "mem-shared-001", "Shared handle", "The workspace room is shared across agents and must not deadlock.")

	done := make(chan error, 1)
	go func() {
		// First holder opens the scorch index (real bbolt open).
		ri1, err := memindex.OpenOrCreate(room)
		if err != nil {
			done <- err
			return
		}
		defer ri1.Close()

		// Second holder of the SAME path — pre-fix this call deadlocked forever
		// in a second bbolt.Open. With the registry it reuses ri1's open handle.
		ri2, err := memindex.OpenOrCreate(room)
		if err != nil {
			done <- err
			return
		}
		defer ri2.Close()

		if ri1 != ri2 {
			done <- errDifferentHandles
			return
		}

		// Both holders build + search concurrently; scorch is goroutine-safe for
		// concurrent reads + a single serialised writer (RoomIndex.mu).
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, ri := range []*memindex.RoomIndex{ri1, ri2} {
			wg.Add(1)
			go func(r *memindex.RoomIndex) {
				defer wg.Done()
				if rbErr := r.Rebuild(); rbErr != nil {
					errs <- rbErr
					return
				}
				if _, sErr := r.Search("workspace", 10); sErr != nil {
					errs <- sErr
				}
			}(ri)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shared-handle open/build/search failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("DEADLOCK REGRESSION: two OpenOrCreate calls on the same room path did not complete within 30s")
	}
}

// errDifferentHandles signals the registry returned distinct handles for the
// same path (the registry must return one shared *RoomIndex per path).
var errDifferentHandles = errors.New("two OpenOrCreate calls on the same path returned different *RoomIndex handles (not shared)")
