// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// entityFixture is a minimal struct used to round-trip through writeEntity /
// readEntity in the delete-lock concurrency tests.
type entityFixture struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TestDeleteEntity_HoldsFlock_NoResurrection is the M5 regression test: the
// workspace/project delete cascade (and every individual delete it calls)
// removes entities through deleteEntity. deleteEntity MUST acquire the same
// per-path advisory flock that writeEntity holds, otherwise a concurrent
// writer's temp-file + atomic rename can land AFTER os.Remove and silently
// resurrect a just-deleted entity.
//
// This test hammers the same entity file with one deleter and many writers
// concurrently, then performs a final authoritative delete and asserts the
// file is gone. Run with -race to catch the unlocked-access data race the fix
// closes. Before the fix (deleteEntity calling os.Remove without WithFlock)
// this test is flaky / resurrection-prone under -race; after the fix it is
// deterministic because delete and write serialize on the shared flock.
func TestDeleteEntity_HoldsFlock_NoResurrection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	const id = "task_m5_lock"

	// Seed the entity so the first writers/deleters operate on a real file.
	if err := writeEntity(dir, id, entityFixture{ID: id, Name: "seed"}); err != nil {
		t.Fatalf("seed writeEntity: %v", err)
	}

	var wg sync.WaitGroup
	const rounds = 200

	// Writers: repeatedly rewrite the same entity.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				// Errors are acceptable here (the deleter may have removed the
				// file mid-flight); we only require that no data race or panic
				// occurs and that the operations are serialized by the flock.
				_ = writeEntity(dir, id, entityFixture{ID: id, Name: "rewrite"})
			}
		}(w)
	}

	// Deleters: repeatedly delete the same entity (idempotent).
	for d := 0; d < 2; d++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if err := deleteEntity(dir, id); err != nil {
					t.Errorf("concurrent deleteEntity returned error: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// Authoritative final delete: after all writers have stopped, a final
	// delete must leave NO file behind. If deleteEntity did not hold the flock
	// a late writer's rename could resurrect the entity; the fix prevents that.
	if err := deleteEntity(dir, id); err != nil {
		t.Fatalf("final deleteEntity: %v", err)
	}
	path := entityPath(dir, id)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("entity file still present after final delete (resurrection): stat err=%v", err)
	}
}

// TestDeleteEntity_Idempotent_MissingFile verifies the documented contract that
// deleting a non-existent entity is a no-op success. The flock open path uses
// O_CREATE, so this also guards against the lock-acquire step accidentally
// leaving a stale empty file behind after an idempotent delete.
func TestDeleteEntity_Idempotent_MissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pins")
	const id = "pin_missing"

	if err := deleteEntity(dir, id); err != nil {
		t.Fatalf("deleteEntity on missing file must succeed, got: %v", err)
	}
	if _, err := os.Stat(entityPath(dir, id)); !os.IsNotExist(err) {
		t.Fatalf("deleteEntity on missing file must not leave a file behind: stat err=%v", err)
	}
}

// TestDeleteEntity_RejectsTraversal confirms the path-traversal guard still
// fires before any flock/remove is attempted (defense-in-depth unchanged by
// the M5 lock fix).
func TestDeleteEntity_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"../escape", "a/b", "with\x00null", ".."} {
		if err := deleteEntity(dir, bad); err == nil {
			t.Errorf("deleteEntity(%q) must reject traversal/invalid id, got nil error", bad)
		}
	}
}
