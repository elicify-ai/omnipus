// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	_ "unsafe" // Required for the test-only alias to the existing filesystem seam.

	"github.com/elicify-ai/omnipus/pkg/task"
)

// task.InterceptAtomicWritesForTest is compiled only into task's test binary,
// not when gateway imports task. Alias its EXISTING writer seam in this test
// binary instead of adding a production test hook, making a store public or
// replacing StartTaskNow. The signature matches task/atomic_write_export_test.go.
// All unrelated writes delegate to the original real atomic writer.
//
//go:linkname gatewayTaskAtomicWriter github.com/elicify-ai/omnipus/pkg/task.writeFileAtomicFn
var gatewayTaskAtomicWriter func(string, []byte, os.FileMode) error

// restBindingWriteFault follows task/task_session_binding_fault_test.go's
// filesystem-only interception: reject every new nonempty binding on one task,
// but permit status/result writes (especially the executor's Failed write and
// the caller's rollback). Serial tests only; never call t.Parallel.
type restBindingWriteFault struct {
	mu             sync.Mutex
	path           string
	cause          error
	bindings       []string
	persistedFails []task.Task
}

func installRESTBindingWriteFault(t *testing.T) *restBindingWriteFault {
	t.Helper()
	f := &restBindingWriteFault{cause: errors.New("RED1026: REST session binding persistence failed")}
	original := gatewayTaskAtomicWriter
	if original == nil {
		t.Fatal("BLOCKED: existing task atomic-writer seam unavailable — required by #1026 real REST caller regression")
	}
	gatewayTaskAtomicWriter = func(path string, data []byte, perm os.FileMode) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if path != f.path {
			return original(path, data, perm)
		}
		var attempted task.Task
		if err := json.Unmarshal(data, &attempted); err != nil {
			return fmt.Errorf("REST binding fault fixture: decode attempted task: %w", err)
		}
		// Failed is writable: record it ONLY after the real atomic write succeeds.
		// This distinguishes caller rollback from a Failed disposition that
		// could not be persisted in the first place.
		if attempted.Status == task.StatusFailed {
			if err := original(path, data, perm); err != nil {
				return err
			}
			f.persistedFails = append(f.persistedFails, attempted)
			return nil
		}
		if attempted.SessionID != "" {
			priorData, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("REST binding fault fixture: read durable task: %w", err)
			}
			var prior task.Task
			if err := json.Unmarshal(priorData, &prior); err != nil {
				return fmt.Errorf("REST binding fault fixture: decode durable task: %w", err)
			}
			if attempted.SessionID != prior.SessionID {
				f.bindings = append(f.bindings, attempted.SessionID)
				return f.cause
			}
		}
		return original(path, data, perm)
	}
	// Install BEFORE constructing the loop: LIFO cleanup closes/drains it
	// before restoring this package-global writer, just like the task fixture.
	t.Cleanup(func() { gatewayTaskAtomicWriter = original })
	return f
}

func (f *restBindingWriteFault) target(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.path = path
}

func (f *restBindingWriteFault) snapshot() ([]string, []task.Task) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bindings...), append([]task.Task(nil), f.persistedFails...)
}
