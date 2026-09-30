// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

type bindingFaultMode uint8

const (
	bindingHealthy bindingFaultMode = iota
	bindingFailFirst
	bindingFailAlways
)

type bindingWriteAttempt struct {
	sessionID string
	err       error
}

type bindingFaultSnapshot struct {
	bindings     []bindingWriteAttempt
	failedWrites []task.Task
}

// bindingWriteFault intercepts only the actual filesystem write for the target
// task. Store validation, locks, reads and every unrelated write remain real.
// A binding attempt changes a nonempty SessionID relative to the durable task;
// subsequent updates carrying an already-persisted binding are not retries.
// Tests using the package-global writer seam must not call t.Parallel.
type bindingWriteFault struct {
	mu               sync.Mutex
	path             string
	mode             bindingFaultMode
	failDisposition  bool
	bindingCause     error
	dispositionCause error
	bindings         []bindingWriteAttempt
	failedWrites     []task.Task
}

func installBindingWriteFault(t *testing.T, mode bindingFaultMode, failDisposition bool) *bindingWriteFault {
	t.Helper()
	f := &bindingWriteFault{
		mode: mode, failDisposition: failDisposition,
		bindingCause:     errors.New("RED1026: session binding persistence failed"),
		dispositionCause: errors.New("RED1026: Failed disposition persistence failed"),
	}
	task.InterceptAtomicWritesForTest(t, f.write)
	return f
}

func (f *bindingWriteFault) target(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.path = path
}

func (f *bindingWriteFault) write(path string, data []byte, perm os.FileMode, next task.AtomicWriteFuncForTest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path != f.path {
		return next(path, data, perm)
	}
	var attempted task.Task
	if err := json.Unmarshal(data, &attempted); err != nil {
		return fmt.Errorf("binding fault fixture: decode attempted task: %w", err)
	}
	if attempted.Status == task.StatusFailed {
		f.failedWrites = append(f.failedWrites, attempted)
		if f.failDisposition {
			return f.dispositionCause
		}
		return next(path, data, perm)
	}
	if attempted.SessionID == "" {
		return next(path, data, perm)
	}
	priorData, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("binding fault fixture: read durable prior task: %w", err)
	}
	var prior task.Task
	if err := json.Unmarshal(priorData, &prior); err != nil {
		return fmt.Errorf("binding fault fixture: decode durable prior task: %w", err)
	}
	if attempted.SessionID == prior.SessionID {
		return next(path, data, perm)
	}
	// The ruling requires one retry, not a new session or an unrelated write.
	fail := f.mode == bindingFailAlways || (f.mode == bindingFailFirst && len(f.bindings) == 0)
	if fail {
		err = f.bindingCause
	} else {
		err = next(path, data, perm)
	}
	f.bindings = append(f.bindings, bindingWriteAttempt{sessionID: attempted.SessionID, err: err})
	return err
}

func (f *bindingWriteFault) snapshot() bindingFaultSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bindingFaultSnapshot{
		bindings:     append([]bindingWriteAttempt(nil), f.bindings...),
		failedWrites: append([]task.Task(nil), f.failedWrites...),
	}
}
